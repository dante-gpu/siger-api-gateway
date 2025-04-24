package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"siger-api-gateway/internal"
	"siger-api-gateway/internal/storage"
)

// JobStore interface for interacting with the job storage
type JobStore interface {
	AddJob(job storage.JobInfo)
	GetJob(id string) (storage.JobInfo, error)
	UpdateJobStatus(id string, status storage.JobStatus, message string) error
}

// NATSClient is a client for connecting to NATS
// Encapsulates both core NATS and JetStream functionality
// Added error recovery for both operations - virjilakrum
type NATSClient struct {
	conn        *nats.Conn
	js          jetstream.JetStream
	logger      internal.LoggerInterface
	jobStore    *storage.JobStore
	initialized bool
	config      NATSConfig
}

// NATSConfig holds configuration for the NATS client
// Separated from the main config for cleaner code organization
// Makes it easier to run with different NATS clusters - virjilakrum
type NATSConfig struct {
	URL      string `yaml:"url"`
	Stream   string `yaml:"stream"`
	MaxAge   string `yaml:"maxAge"`
	Replicas int    `yaml:"replicas"`
}

// JobStatusUpdate represents a status update for a job
// Used for communication between workers and the API gateway
// This replaces our old manual status polling - virjilakrum
type JobStatusUpdate struct {
	JobID     string    `json:"job_id"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Progress  float64   `json:"progress,omitempty"` // 0-100 percent
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

// NewNATSClient creates a new NATS client
// Now initialized without job store to avoid circular dependency
// Job store can be set later with SetJobStore - virjilakrum
func NewNATSClient(config NATSConfig, logger internal.LoggerInterface) (*NATSClient, error) {
	// Validate config
	if config.URL == "" {
		return nil, errors.New("NATS URL is required")
	}
	if config.Stream == "" {
		return nil, errors.New("NATS stream is required")
	}

	client := &NATSClient{
		logger: logger,
		config: config,
	}

	// Connect to NATS
	opts := []nats.Option{
		nats.Name("siger-api-gateway"),
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			if client.logger != nil {
				client.logger.Warnf("Disconnected from NATS: %v", err)
			} else {
				log.Printf("Disconnected from NATS: %v", err)
			}
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			if client.logger != nil {
				client.logger.Infof("Reconnected to NATS server: %s", nc.ConnectedUrl())
			} else {
				log.Printf("Reconnected to NATS server: %s", nc.ConnectedUrl())
			}
		}),
		nats.ErrorHandler(func(nc *nats.Conn, sub *nats.Subscription, err error) {
			if client.logger != nil {
				client.logger.Errorf("Error in NATS connection: %v", err)
			} else {
				log.Printf("Error in NATS connection: %v", err)
			}
		}),
	}

	var err error
	client.conn, err = nats.Connect(config.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Create JetStream context
	client.js, err = jetstream.New(client.conn)
	if err != nil {
		client.conn.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	client.initialized = true
	return client, nil
}

// SetJobStore sets the job store for the NATS client
// This allows status updates to be persisted in the job store
// Called after both components are initialized - virjilakrum
func (c *NATSClient) SetJobStore(jobStore *storage.JobStore) {
	c.jobStore = jobStore
}

// EnsureStream ensures that the stream exists
// Critical for ensuring our job messages are persisted
// Uses MaxAge to prevent infinite storage growth - virjilakrum
func (c *NATSClient) EnsureStream(subjects []string) error {
	if !c.initialized {
		return errors.New("NATS client not initialized")
	}

	// Parse max age duration
	maxAge, err := time.ParseDuration(c.config.MaxAge)
	if err != nil {
		return fmt.Errorf("invalid max age duration: %w", err)
	}

	// Create or update the stream
	_, err = c.js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
		Name:        c.config.Stream,
		Description: "Stream for job processing",
		Subjects:    subjects,
		MaxAge:      maxAge,
		Replicas:    c.config.Replicas,
		Storage:     jetstream.FileStorage,
	})
	return err
}

// Publish publishes a message to NATS
// Simple wrapper around the NATS Publish method
// Added type safety with mandatory serialization - virjilakrum
func (c *NATSClient) Publish(subject string, message interface{}) error {
	if !c.initialized {
		return errors.New("NATS client not initialized")
	}

	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	return c.conn.Publish(subject, data)
}

// Subscribe subscribes to a NATS subject with a message handler
// Used for simpler pub/sub use cases without persistence
// Message handling done in separate goroutine for safety - virjilakrum
func (c *NATSClient) Subscribe(subject string, handler func([]byte)) error {
	if !c.initialized {
		return errors.New("NATS client not initialized")
	}

	_, err := c.conn.Subscribe(subject, func(msg *nats.Msg) {
		// Handle the message in a goroutine to prevent blocking NATS
		go func() {
			defer func() {
				if r := recover(); r != nil {
					c.logger.Errorf("Panic in NATS message handler: %v", r)
				}
			}()
			handler(msg.Data)
		}()
	})
	return err
}

// PublishToStream publishes a message to the JetStream
// Returns the server acknowledgment for confirmed delivery
// Critical for reliable job submission - virjilakrum
func (c *NATSClient) PublishToStream(subject string, message interface{}) (*jetstream.PubAck, error) {
	if !c.initialized {
		return nil, errors.New("NATS client not initialized")
	}

	data, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message: %w", err)
	}

	ack, err := c.js.Publish(context.Background(), subject, data)
	if err != nil {
		return nil, fmt.Errorf("failed to publish to stream: %w", err)
	}

	return ack, nil
}

// SubscribeToStatusUpdates subscribes to job status updates
// Updates the job store with the latest status
// This is the key integration between worker nodes and API gateway - virjilakrum
func (c *NATSClient) SubscribeToStatusUpdates() error {
	if !c.initialized {
		return errors.New("NATS client not initialized")
	}

	if c.jobStore == nil {
		return errors.New("job store not set, cannot subscribe to status updates")
	}

	_, err := c.conn.Subscribe("jobs.status", func(msg *nats.Msg) {
		// Handle the message in a goroutine to prevent blocking NATS
		go func() {
			defer func() {
				if r := recover(); r != nil {
					c.logger.Errorf("Panic in status update handler: %v", r)
				}
			}()

			var update JobStatusUpdate
			if err := json.Unmarshal(msg.Data, &update); err != nil {
				c.logger.Errorf("Failed to unmarshal status update: %v", err)
				return
			}

			// Convert to job store status
			var status storage.JobStatus
			switch update.Status {
			case "queued":
				status = storage.JobStatusQueued
			case "processing":
				status = storage.JobStatusProcessing
			case "completed":
				status = storage.JobStatusCompleted
			case "failed":
				status = storage.JobStatusFailed
			case "cancelled":
				status = storage.JobStatusCancelled
			default:
				c.logger.Warnf("Unknown job status: %s", update.Status)
				return
			}

			// Update job in store
			err := c.jobStore.UpdateJobStatus(update.JobID, status, update.Message)
			if err != nil {
				c.logger.Warnf("Failed to update job status: %v", err)
				return
			}

			// Get current job info to update timestamps
			jobInfo, err := c.jobStore.GetJob(update.JobID)
			if err != nil {
				c.logger.Warnf("Failed to get job for timestamp update: %v", err)
				return
			}

			// Update timestamps
			if !update.StartedAt.IsZero() {
				jobInfo.StartedAt = update.StartedAt
				c.jobStore.AddJob(jobInfo) // Re-add the job with updated timestamps
			}

			if !update.EndedAt.IsZero() {
				jobInfo.CompletedAt = update.EndedAt
				c.jobStore.AddJob(jobInfo) // Re-add the job with updated timestamps
			}

			c.logger.Infof("Updated job status: id=%s status=%s", update.JobID, update.Status)
		}()
	})

	return err
}

// Close closes the NATS connection
// Always called during server shutdown
// Ensure all in-flight messages are delivered - virjilakrum
func (c *NATSClient) Close() {
	if c.conn != nil {
		c.conn.Drain()
		c.conn.Close()
	}
}
