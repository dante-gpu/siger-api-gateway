package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"siger-api-gateway/internal"
	"siger-api-gateway/internal/discovery"
	"siger-api-gateway/internal/metrics"
)

// ProxyHandler provides reverse proxy functionality to backend services
// This is the heart of our API Gateway - dynamic service-based routing
// We used to use Nginx but needed more programmatic control - virjilakrum
type ProxyHandler struct {
	serviceRegistry *discovery.ServiceRegistry
	loadBalancers   map[string]*discovery.LoadBalancer
	logger          internal.LoggerInterface
}

// NewProxyHandler creates a new proxy handler
// Keeping this simple since most complexity is in the HandleProxy method
// We initially had more parameters but simplified for maintainability - virjilakrum
func NewProxyHandler(serviceRegistry *discovery.ServiceRegistry) *ProxyHandler {
	return &ProxyHandler{
		serviceRegistry: serviceRegistry,
		loadBalancers:   make(map[string]*discovery.LoadBalancer),
		logger:          internal.Logger,
	}
}

// HandleProxy returns a handler that proxies requests to the specified service
// Implements service discovery, load balancing, and instrumentation in one place
// This took several iterations to get right - early versions lacked proper error handling - virjilakrum
func (ph *ProxyHandler) HandleProxy(serviceName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Get service instance using load balancer
		instance, err := ph.getServiceInstance(serviceName)
		if err != nil {
			ph.logger.Errorw("Failed to get service instance", "service", serviceName, "error", err)
			http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
			return
		}

		// Track active connection
		// This is crucial for proper load balancing - prevents routing to instances
		// that are already overloaded with requests - virjilakrum
		ph.loadBalancers[serviceName].InstanceBegin(instance.ID)
		defer ph.loadBalancers[serviceName].InstanceEnd(instance.ID)

		// Construct the target URL
		targetURL := url.URL{
			Scheme: "http", // Assuming HTTP, could be configurable
			Host:   fmt.Sprintf("%s:%d", instance.Address, instance.Port),
			Path:   r.URL.Path,
		}

		// Create a reverse proxy
		// Using standard lib's httputil - considered nginx-proxy and others
		// but this gives us the most control and lowest overhead - virjilakrum
		proxy := httputil.NewSingleHostReverseProxy(&targetURL)

		// Customize the director to modify the request before sending it to the backend
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)

			// Preserve the original Host header (or set a specific one if needed)
			// req.Host = targetURL.Host
			// Note: uncomment above to override host header - useful for services
			// that validate the Host header for security - virjilakrum

			// Add X-Forwarded headers if not present
			if _, ok := req.Header["X-Forwarded-For"]; !ok {
				req.Header.Set("X-Forwarded-For", r.RemoteAddr)
			}
			if _, ok := req.Header["X-Forwarded-Proto"]; !ok {
				if r.TLS == nil {
					req.Header.Set("X-Forwarded-Proto", "http")
				} else {
					req.Header.Set("X-Forwarded-Proto", "https")
				}
			}

			// Add a custom header to indicate the request came through the gateway
			// This helps services know they're behind our gateway and can apply
			// different logic if needed - virjilakrum
			req.Header.Set("X-Gateway", "siger-api-gateway")

			ph.logger.Debugw("Proxying request",
				"service", serviceName,
				"instance", instance.ID,
				"target", targetURL.String(),
				"method", req.Method,
				"path", req.URL.Path,
			)
		}

		// Customize the error handler
		// Proper error handling here saves hours of debugging
		// We log everything and return a clean error to clients - virjilakrum
		proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
			ph.logger.Errorw("Proxy error",
				"service", serviceName,
				"instance", instance.ID,
				"target", targetURL.String(),
				"method", req.Method,
				"path", req.URL.Path,
				"error", err,
			)

			metrics.UpstreamRequestsTotal.WithLabelValues(serviceName, "error").Inc()
			rw.WriteHeader(http.StatusBadGateway)
			rw.Write([]byte("Bad Gateway"))
		}

		// Proxy the request
		proxy.ServeHTTP(w, r)

		// Record metrics
		// These are critical for our SLOs and monitoring
		// We rely on these for capacity planning - virjilakrum
		duration := time.Since(start).Seconds()
		metrics.UpstreamRequestDuration.WithLabelValues(serviceName).Observe(duration)
		metrics.UpstreamRequestsTotal.WithLabelValues(serviceName, "success").Inc()
	}
}

// getServiceInstance gets a service instance using a load balancer
// If a load balancer for the service doesn't exist, it creates one
// This lazy initialization approach simplifies our startup process - virjilakrum
func (ph *ProxyHandler) getServiceInstance(serviceName string) (discovery.ServiceInstance, error) {
	// Check if we already have a load balancer for this service
	if _, exists := ph.loadBalancers[serviceName]; !exists {
		// Get all instances of the service
		instances, err := ph.serviceRegistry.DiscoverService(serviceName)
		if err != nil {
			return discovery.ServiceInstance{}, fmt.Errorf("failed to discover service %s: %w", serviceName, err)
		}

		if len(instances) == 0 {
			return discovery.ServiceInstance{}, fmt.Errorf("no instances available for service %s", serviceName)
		}

		// Create a new load balancer for the service using Round Robin as default
		// Tried weighted and least connections algorithms too, but RR with
		// connection tracking works best for our workload - virjilakrum
		ph.loadBalancers[serviceName] = discovery.NewLoadBalancer(discovery.RoundRobin, instances)

		// Start watching for service changes
		go ph.watchServiceChanges(serviceName)
	}

	// Get an instance using the load balancer
	instance, err := ph.loadBalancers[serviceName].GetInstance()
	if err != nil {
		return discovery.ServiceInstance{}, fmt.Errorf("load balancer failed to get instance: %w", err)
	}

	return instance, nil
}

// watchServiceChanges watches for changes in the service and updates the load balancer
// This is what makes our gateway truly dynamic - instances can come and go
// and the gateway adjusts without any restarts or downtime - virjilakrum
func (ph *ProxyHandler) watchServiceChanges(serviceName string) {
	// 30 seconds is the max time for a blocking query - tuned after testing
	// Too short: excessive API calls, Too long: stale data - virjilakrum
	instancesChan, errChan := ph.serviceRegistry.WatchService(serviceName, 30*time.Second)

	for {
		select {
		case instances := <-instancesChan:
			// Update the load balancer with the new instances
			ph.loadBalancers[serviceName].UpdateInstances(instances)
			ph.logger.Infof("Updated load balancer for service %s with %d instances", serviceName, len(instances))
		case err := <-errChan:
			ph.logger.Errorw("Error watching service", "service", serviceName, "error", err)
		}
	}
}
