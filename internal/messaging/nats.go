package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	"siger-api-gateway/internal"
)

// NATSClient provides messaging functionality using NATS
type NATSClient struct {
	conn   *nats.Conn
	js     nats.JetStreamContext
	logger internal.LoggerInterface
}

// NewNATSClient creates a new NATS client
func NewNATSClient(natsAddress string) (*NATSClient, error) {
	// Connect to NATS
	conn, err := nats.Connect(natsAddress,
		nats.Timeout(5*time.Second),
		nats.PingInterval(20*time.Second),
		nats.MaxPingsOutstanding(5),
		nats.MaxReconnects(-1), // Unlimited reconnects
		nats.ReconnectWait(1*time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			internal.Logger.Warnf("NATS disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			internal.Logger.Infof("NATS reconnected to %s", nc.ConnectedUrl())
		}),
		nats.ErrorHandler(func(nc *nats.Conn, sub *nats.Subscription, err error) {
			internal.Logger.Errorf("NATS error: %v", err)
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Create JetStream context
	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	client := &NATSClient{
		conn:   conn,
		js:     js,
		logger: internal.Logger,
	}

	internal.Logger.Infof("Connected to NATS server at %s", natsAddress)
	return client, nil
}

// Close closes the NATS connection
func (nc *NATSClient) Close() {
	if nc.conn != nil {
		nc.conn.Close()
		nc.logger.Info("NATS connection closed")
	}
}

// Publish publishes a message to a subject
func (nc *NATSClient) Publish(subject string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal message data: %w", err)
	}

	err = nc.conn.Publish(subject, jsonData)
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	nc.logger.Debugf("Published message to subject %s", subject)
	return nil
}

// PublishWithContext publishes a message with a context for timeout/cancellation
func (nc *NATSClient) PublishWithContext(ctx context.Context, subject string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal message data: %w", err)
	}

	// Create a channel to signal completion
	done := make(chan error, 1)

	go func() {
		err := nc.conn.Publish(subject, jsonData)
		done <- err
	}()

	// Wait for completion or context cancellation
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("failed to publish message: %w", err)
		}
		nc.logger.Debugf("Published message to subject %s", subject)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("context canceled while publishing message: %w", ctx.Err())
	}
}

// PublishAsync publishes a message asynchronously
func (nc *NATSClient) PublishAsync(subject string, data interface{}, cb func(error)) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		if cb != nil {
			cb(fmt.Errorf("failed to marshal message data: %w", err))
		}
		return
	}

	go func() {
		err := nc.conn.Publish(subject, jsonData)
		if err != nil {
			if cb != nil {
				cb(fmt.Errorf("failed to publish message: %w", err))
			}
			return
		}

		nc.logger.Debugf("Published message to subject %s", subject)
		if cb != nil {
			cb(nil)
		}
	}()
}

// QueueSubscribe subscribes to a subject with a queue group
func (nc *NATSClient) QueueSubscribe(subject, queue string, handler func([]byte) error) (*nats.Subscription, error) {
	sub, err := nc.conn.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
		err := handler(msg.Data)
		if err != nil {
			nc.logger.Errorf("Error handling message on %s: %v", subject, err)
		}
	})

	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to %s: %w", subject, err)
	}

	nc.logger.Infof("Subscribed to %s with queue group %s", subject, queue)
	return sub, nil
}

// CreateStream creates a JetStream stream if it doesn't exist
func (nc *NATSClient) CreateStream(name string, subjects []string) error {
	// Check if the stream already exists
	_, err := nc.js.StreamInfo(name)
	if err == nil {
		nc.logger.Debugf("Stream %s already exists", name)
		return nil
	}

	// Create the stream
	_, err = nc.js.AddStream(&nats.StreamConfig{
		Name:     name,
		Subjects: subjects,
		MaxAge:   24 * time.Hour, // Messages expire after 24 hours
		Storage:  nats.FileStorage,
	})

	if err != nil {
		return fmt.Errorf("failed to create stream %s: %w", name, err)
	}

	nc.logger.Infof("Created stream %s with subjects %v", name, subjects)
	return nil
}

// PublishToStream publishes a message to a JetStream stream
func (nc *NATSClient) PublishToStream(subject string, data interface{}) (*nats.PubAck, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message data: %w", err)
	}

	pubAck, err := nc.js.Publish(subject, jsonData)
	if err != nil {
		return nil, fmt.Errorf("failed to publish message to stream: %w", err)
	}

	nc.logger.Debugf("Published message to stream subject %s, sequence %d", subject, pubAck.Sequence)
	return pubAck, nil
}

// SubscribeToStream subscribes to a JetStream stream
func (nc *NATSClient) SubscribeToStream(subject, consumer string, handler func([]byte) error) (*nats.Subscription, error) {
	sub, err := nc.js.QueueSubscribe(subject, consumer, func(msg *nats.Msg) {
		err := handler(msg.Data)
		if err != nil {
			nc.logger.Errorf("Error handling message on %s: %v", subject, err)
			// Negative acknowledge to reprocess later
			msg.Nak()
		} else {
			// Acknowledge successful processing
			msg.Ack()
		}
	}, nats.Durable(consumer), nats.AckExplicit())

	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to stream %s: %w", subject, err)
	}

	nc.logger.Infof("Subscribed to stream %s with consumer %s", subject, consumer)
	return sub, nil
}
