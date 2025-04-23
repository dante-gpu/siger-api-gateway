package discovery

import (
	"fmt"
	"time"

	"github.com/hashicorp/consul/api"

	"siger-api-gateway/internal"
)

// ServiceRegistry provides service registration and discovery functionality
type ServiceRegistry struct {
	client *api.Client
	logger internal.LoggerInterface
}

// ServiceInstance represents a service instance with its address and metadata
type ServiceInstance struct {
	ID          string
	ServiceName string
	Address     string
	Port        int
	Healthy     bool
	Metadata    map[string]string
}

// NewServiceRegistry creates a new service registry client
func NewServiceRegistry(consulAddress string) (*ServiceRegistry, error) {
	config := api.DefaultConfig()
	config.Address = consulAddress

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
func (sr *ServiceRegistry) Register(
	id string,
	name string,
	address string,
	port int,
	tags []string,
	meta map[string]string,
) error {
	// Define the health check
	check := &api.AgentServiceCheck{
		HTTP:                           fmt.Sprintf("http://%s:%d/health", address, port),
		Interval:                       "10s",
		Timeout:                        "5s",
		DeregisterCriticalServiceAfter: "30s",
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
func (sr *ServiceRegistry) Deregister(id string) error {
	err := sr.client.Agent().ServiceDeregister(id)
	if err != nil {
		return fmt.Errorf("failed to deregister service: %w", err)
	}

	sr.logger.Infof("Service deregistered from Consul: id=%s", id)
	return nil
}

// DiscoverService finds all instances of a service
func (sr *ServiceRegistry) DiscoverService(serviceName string) ([]ServiceInstance, error) {
	// Query for service health to get only healthy instances
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
func (sr *ServiceRegistry) WatchService(serviceName string, updateInterval time.Duration) (<-chan []ServiceInstance, <-chan error) {
	instancesChan := make(chan []ServiceInstance)
	errChan := make(chan error)

	go func() {
		defer close(instancesChan)
		defer close(errChan)

		var lastIndex uint64

		for {
			// Query service health with blocking query
			serviceEntries, meta, err := sr.client.Health().Service(serviceName, "", true, &api.QueryOptions{
				WaitIndex: lastIndex,
				WaitTime:  updateInterval,
			})

			if err != nil {
				errChan <- fmt.Errorf("error watching service %s: %w", serviceName, err)
				time.Sleep(time.Second) // Wait before retrying
				continue
			}

			// Update the last index for the next blocking query
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
