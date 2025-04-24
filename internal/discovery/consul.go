package discovery

import (
	"fmt"
	"time"

	"github.com/hashicorp/consul/api"

	"siger-api-gateway/internal"
)

// ServiceRegistry provides service registration and discovery functionality
// We chose Consul over etcd because it has better health checking features
// and the UI is actually useful for debugging - virjilakrum
type ServiceRegistry struct {
	client *api.Client
	logger internal.LoggerInterface
}

// ServiceInstance represents a service instance with its address and metadata
// Added Metadata map for service versioning and feature flagging
// This saves us from having to deploy new instances for simple config changes - virjilakrum
type ServiceInstance struct {
	ID          string
	ServiceName string
	Address     string
	Port        int
	Healthy     bool
	Metadata    map[string]string
}

// NewServiceRegistry creates a new service registry client
// Tried to make this as simple as possible - configuration complexity belongs in Consul itself
// We deliberately avoid too many options here to keep the API clean - virjilakrum
func NewServiceRegistry(consulAddress string) (*ServiceRegistry, error) {
	if consulAddress == "" {
		return nil, fmt.Errorf("consul address is required")
	}

	config := api.DefaultConfig()
	config.Address = consulAddress

	// Default connection timeout is fine for LAN but too slow for containerized environments
	// Lowering the timeouts to catch network issues faster - virjilakrum
	config.HttpClient.Timeout = 5 * time.Second

	client, err := api.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Consul client: %w", err)
	}

	return &ServiceRegistry{
		client: client,
		logger: internal.Logger,
	}, nil
}

// Register registers the current service with Consul
// Using HTTP health checks instead of TTL because they're more reliable
// We had issues with TTL checks in high-load situations - virjilakrum
func (sr *ServiceRegistry) Register(
	id string,
	name string,
	address string,
	port int,
	tags []string,
	meta map[string]string,
) error {
	if sr == nil || sr.client == nil {
		return fmt.Errorf("service registry not properly initialized")
	}

	// Define the health check
	// Experimented with different intervals - 10s is the sweet spot between
	// responsiveness and network overhead - virjilakrum
	check := &api.AgentServiceCheck{
		HTTP:                           fmt.Sprintf("http://%s:%d/health", address, port),
		Interval:                       "10s",
		Timeout:                        "5s",
		DeregisterCriticalServiceAfter: "30s", // Increased from 15s to reduce flapping
	}

	// Create registration
	registration := &api.AgentServiceRegistration{
		ID:      id,
		Name:    name,
		Address: address,
		Port:    port,
		Tags:    tags,
		Meta:    meta,
		Check:   check,
	}

	// Register the service
	err := sr.client.Agent().ServiceRegister(registration)
	if err != nil {
		return fmt.Errorf("failed to register service: %w", err)
	}

	sr.logger.Infof("Service registered with Consul: id=%s name=%s address=%s:%d", id, name, address, port)
	return nil
}

// Deregister removes the service from Consul
// Important for clean shutdowns, otherwise Consul keeps zombie services around
// This was a major source of routing errors before we fixed it - virjilakrum
func (sr *ServiceRegistry) Deregister(id string) error {
	if sr == nil || sr.client == nil {
		return fmt.Errorf("service registry not properly initialized")
	}

	err := sr.client.Agent().ServiceDeregister(id)
	if err != nil {
		return fmt.Errorf("failed to deregister service: %w", err)
	}

	sr.logger.Infof("Service deregistered from Consul: id=%s", id)
	return nil
}

// DiscoverService finds all instances of a service
// Only returns healthy instances - this is key for proper load balancing
// Unhealthy instances would cause timeout errors and circuit breaking - virjilakrum
func (sr *ServiceRegistry) DiscoverService(serviceName string) ([]ServiceInstance, error) {
	if sr == nil || sr.client == nil {
		return nil, fmt.Errorf("service registry not properly initialized")
	}

	// Query for service health to get only healthy instances
	// The empty string as the second parameter means "any tag"
	// Third parameter true means only passing health checks - virjilakrum
	serviceEntries, _, err := sr.client.Health().Service(serviceName, "", true, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to discover service %s: %w", serviceName, err)
	}

	if len(serviceEntries) == 0 {
		sr.logger.Warnf("No healthy instances found for service: %s", serviceName)
		return []ServiceInstance{}, nil
	}

	// Convert to our ServiceInstance type
	var instances []ServiceInstance
	for _, entry := range serviceEntries {
		instance := ServiceInstance{
			ID:          entry.Service.ID,
			ServiceName: entry.Service.Service,
			Address:     entry.Service.Address,
			Port:        entry.Service.Port,
			Healthy:     true,
			Metadata:    entry.Service.Meta,
		}
		instances = append(instances, instance)
	}

	sr.logger.Debugf("Discovered %d instances of service %s", len(instances), serviceName)
	return instances, nil
}

// WatchService watches for changes in a service and returns updates through a channel
// This is crucial for our dynamic routing - when instances come and go, routes update
// Much better than periodic polling which has lag and unnecessary API calls - virjilakrum
func (sr *ServiceRegistry) WatchService(serviceName string, updateInterval time.Duration) (<-chan []ServiceInstance, <-chan error) {
	instancesChan := make(chan []ServiceInstance)
	errChan := make(chan error)

	go func() {
		defer close(instancesChan)
		defer close(errChan)

		var lastIndex uint64

		for {
			// Query service health with blocking query
			// This is Consul's long-polling mechanism - much more efficient than
			// short polling with sleep intervals - virjilakrum
			serviceEntries, meta, err := sr.client.Health().Service(serviceName, "", true, &api.QueryOptions{
				WaitIndex: lastIndex,
				WaitTime:  updateInterval, // Max duration Consul will wait before responding
			})

			if err != nil {
				errChan <- fmt.Errorf("error watching service %s: %w", serviceName, err)
				time.Sleep(time.Second) // Wait before retrying
				continue
			}

			// Update the last index for the next blocking query
			// This is key to the Consul blocking query mechanism - it tells Consul
			// to only respond when there's a change after this index - virjilakrum
			lastIndex = meta.LastIndex

			// Convert to our ServiceInstance type
			var instances []ServiceInstance
			for _, entry := range serviceEntries {
				instance := ServiceInstance{
					ID:          entry.Service.ID,
					ServiceName: entry.Service.Service,
					Address:     entry.Service.Address,
					Port:        entry.Service.Port,
					Healthy:     true,
					Metadata:    entry.Service.Meta,
				}
				instances = append(instances, instance)
			}

			// Send the updated instances
			instancesChan <- instances
		}
	}()

	return instancesChan, errChan
}
