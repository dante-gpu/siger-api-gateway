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
type ProxyHandler struct {
	serviceRegistry *discovery.ServiceRegistry
	loadBalancers   map[string]*discovery.LoadBalancer
	logger          internal.LoggerInterface
}

// NewProxyHandler creates a new proxy handler
func NewProxyHandler(serviceRegistry *discovery.ServiceRegistry) *ProxyHandler {
	return &ProxyHandler{
		serviceRegistry: serviceRegistry,
		loadBalancers:   make(map[string]*discovery.LoadBalancer),
		logger:          internal.Logger,
	}
}

// HandleProxy returns a handler that proxies requests to the specified service
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
		ph.loadBalancers[serviceName].InstanceBegin(instance.ID)
		defer ph.loadBalancers[serviceName].InstanceEnd(instance.ID)

		// Construct the target URL
		targetURL := url.URL{
			Scheme: "http", // Assuming HTTP, could be configurable
			Host:   fmt.Sprintf("%s:%d", instance.Address, instance.Port),
			Path:   r.URL.Path,
		}

		// Create a reverse proxy
		proxy := httputil.NewSingleHostReverseProxy(&targetURL)

		// Customize the director to modify the request before sending it to the backend
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)

			// Preserve the original Host header (or set a specific one if needed)
			// req.Host = targetURL.Host

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
		duration := time.Since(start).Seconds()
		metrics.UpstreamRequestDuration.WithLabelValues(serviceName).Observe(duration)
		metrics.UpstreamRequestsTotal.WithLabelValues(serviceName, "success").Inc()
	}
}

// getServiceInstance gets a service instance using a load balancer
// If a load balancer for the service doesn't exist, it creates one
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
func (ph *ProxyHandler) watchServiceChanges(serviceName string) {
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
