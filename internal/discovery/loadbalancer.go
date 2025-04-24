package discovery

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// LoadBalancerType defines the type of load balancing algorithm
type LoadBalancerType string

const (
	// RoundRobin distributes requests in a circular order
	RoundRobin LoadBalancerType = "round_robin"

	// Random selects instances randomly
	Random LoadBalancerType = "random"

	// LeastConnections selects the instance with the fewest active connections
	LeastConnections LoadBalancerType = "least_connections"
)

// LoadBalancer provides load balancing functionality for service instances
// Started with a simpler algorithm but added LeastConnections to handle uneven loads
// The atomic counter implementation is crucial for thread safety - virjilakrum
type LoadBalancer struct {
	serviceInstances []ServiceInstance
	instanceLock     sync.RWMutex
	lbType           LoadBalancerType
	counter          uint64 // For atomic operations
	connectionCount  map[string]*uint64
}

// NewLoadBalancer creates a new load balancer with the specified type
// We initialize connection counters for each instance to track active requests
// This was key to preventing overloaded instances from getting new traffic - virjilakrum
func NewLoadBalancer(lbType LoadBalancerType, instances []ServiceInstance) *LoadBalancer {
	// Initialize the connection count map for least connections algorithm
	connectionCount := make(map[string]*uint64)
	for _, instance := range instances {
		var count uint64 = 0
		connectionCount[instance.ID] = &count
	}

	return &LoadBalancer{
		serviceInstances: instances,
		lbType:           lbType,
		counter:          0,
		connectionCount:  connectionCount,
	}
}

// UpdateInstances updates the list of available service instances
// This is called whenever our service discovery detects instance changes
// Critical for dynamically adjusting to new/removed instances without restarts - virjilakrum
func (lb *LoadBalancer) UpdateInstances(instances []ServiceInstance) {
	lb.instanceLock.Lock()
	defer lb.instanceLock.Unlock()

	lb.serviceInstances = instances

	// Update the connection count map
	// Preserving existing connection counts is important to avoid disrupting
	// in-flight requests during instance updates - virjilakrum
	newConnectionCount := make(map[string]*uint64)
	for _, instance := range instances {
		// Keep existing connection counts if the instance already exists
		if counter, exists := lb.connectionCount[instance.ID]; exists {
			newConnectionCount[instance.ID] = counter
		} else {
			var count uint64 = 0
			newConnectionCount[instance.ID] = &count
		}
	}
	lb.connectionCount = newConnectionCount
}

// GetInstance returns the next service instance based on the load balancing algorithm
// Benchmarked all three algorithms - RoundRobin is fastest, but LeastConnections
// provides better distribution under uneven loads - virjilakrum
func (lb *LoadBalancer) GetInstance() (ServiceInstance, error) {
	lb.instanceLock.RLock()
	defer lb.instanceLock.RUnlock()

	if len(lb.serviceInstances) == 0 {
		return ServiceInstance{}, fmt.Errorf("no service instances available")
	}

	var selectedIdx int

	switch lb.lbType {
	case RoundRobin:
		// Increment counter and get next index
		// Using atomic operations to avoid race conditions under high concurrency
		// This was much faster than using a mutex for every counter update - virjilakrum
		count := atomic.AddUint64(&lb.counter, 1)
		selectedIdx = int(count) % len(lb.serviceInstances)

	case Random:
		// Get a random index
		// This algorithm is surprisingly effective for evenly distributed request patterns
		// With sufficient request volume, it approaches RoundRobin performance - virjilakrum
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		selectedIdx = r.Intn(len(lb.serviceInstances))

	case LeastConnections:
		// Find the instance with the least connections
		// This algorithm shines with long-running requests that would
		// otherwise cause load imbalances with simpler algorithms - virjilakrum
		minConnections := uint64(^uint64(0)) // max uint64 value
		for i, instance := range lb.serviceInstances {
			if counter, exists := lb.connectionCount[instance.ID]; exists {
				connections := atomic.LoadUint64(counter)
				if connections < minConnections {
					minConnections = connections
					selectedIdx = i
				}
			}
		}

	default:
		// Default to round robin
		count := atomic.AddUint64(&lb.counter, 1)
		selectedIdx = int(count) % len(lb.serviceInstances)
	}

	return lb.serviceInstances[selectedIdx], nil
}

// InstanceBegin marks the beginning of a request to an instance
// This tracks active connections to each instance which is essential
// for the LeastConnections algorithm to work correctly - virjilakrum
func (lb *LoadBalancer) InstanceBegin(instanceID string) {
	lb.instanceLock.RLock()
	defer lb.instanceLock.RUnlock()

	if counter, exists := lb.connectionCount[instanceID]; exists {
		atomic.AddUint64(counter, 1)
	}
}

// InstanceEnd marks the end of a request to an instance
// Always using deferred calls to ensure this runs even if the request handler panics
// Otherwise we'd have permanent counter skew - virjilakrum
func (lb *LoadBalancer) InstanceEnd(instanceID string) {
	lb.instanceLock.RLock()
	defer lb.instanceLock.RUnlock()

	if counter, exists := lb.connectionCount[instanceID]; exists {
		// Make sure we don't go below zero
		currentVal := atomic.LoadUint64(counter)
		if currentVal > 0 {
			atomic.AddUint64(counter, ^uint64(0)) // Subtract 1 using two's complement
		}
	}
}
