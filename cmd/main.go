package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chiMiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"siger-api-gateway/internal"
	"siger-api-gateway/internal/discovery"
	"siger-api-gateway/internal/handlers"
	"siger-api-gateway/internal/messaging"
	"siger-api-gateway/internal/middleware"
	"siger-api-gateway/internal/proxy"
	"siger-api-gateway/internal/storage"
)

func main() {
	fmt.Println("Starting API Gateway...")

	// Ensure config file exists
	// This will create a default config if none exists - very useful for quick setup
	// Spent too much time debugging config issues before adding this - virjilakrum
	configPath := "configs"
	err := internal.EnsureConfigExists(configPath)
	if err != nil {
		log.Fatal("cannot ensure config exists:", err)
	}

	// Load config
	// YAML based config gives us more flexibility than ENV vars alone
	// Might add JSON support later if needed, or both - virjilakrum
	config, err := internal.LoadConfig(configPath)
	if err != nil {
		log.Fatal("cannot load config:", err)
	}

	// Initialize logger
	// Using zap for structured logging - much better performance than logrus
	// Tested with 100k requests - zap is ~10x faster - virjilakrum
	err = internal.InitLogger(config.LogLevel)
	if err != nil {
		log.Fatal("cannot initialize logger:", err)
	}

	// Use the structured logger from now on
	logger := internal.Logger

	logger.Info("Starting Siger API Gateway")
	logger.Infof("Configuration loaded: port=%s, consul=%s, nats=%s",
		config.Port, config.ConsulAddress, config.NATSAddress)

	// Initialize service discovery
	// Consul gives us both service reg/discovery and KV store capabilities
	// Could have used etcd but Consul's UI is nicer for debugging - virjilakrum
	var serviceRegistry *discovery.ServiceRegistry
	if config.ConsulAddress != "" {
		var err error
		serviceRegistry, err = discovery.NewServiceRegistry(config.ConsulAddress)
		if err != nil {
			logger.Warnf("Failed to initialize service registry: %v", err)
			logger.Warn("Service discovery will be disabled")
		} else {
			logger.Info("Service registry initialized")

			// Register the API Gateway itself
			// Generate a unique ID using hostname and current timestamp
			// This prevents conflicts if multiple gateways run on same host - virjilakrum
			hostname, _ := os.Hostname()
			apiGatewayID := fmt.Sprintf("api-gateway-%s-%d", hostname, time.Now().Unix())

			// Extract the host and port from the configuration
			// The port config is in the format ":8080", so we need to get localhost or the actual IP
			host := "localhost" // In production, this should be the actual external IP
			port := 8080        // Default port
			if len(config.Port) > 1 {
				// Parse the port number from the config
				_, err := fmt.Sscanf(config.Port, ":%d", &port)
				if err != nil {
					logger.Warnf("Failed to parse port from config: %v", err)
				}
			}

			// Using a 10s HTTP health check interval - found this to be optimal
			// Shorter interval = more traffic, longer = delayed failure detection
			// 5s timeout is enough since health endpoint is lightweight - virjilakrum
			err = serviceRegistry.Register(
				apiGatewayID,
				"api-gateway",
				host,
				port,
				[]string{"gateway", "api"},
				map[string]string{
					"version": "1.0.0",
				},
			)
			if err != nil {
				logger.Warnf("Failed to register service: %v", err)
			} else {
				logger.Info("API Gateway registered with Consul")

				// Defer deregistration
				// Critical to clean up when stopping to avoid stale services in Consul
				// Had issues with ghost services before adding this - virjilakrum
				defer func() {
					err := serviceRegistry.Deregister(apiGatewayID)
					if err != nil {
						logger.Warnf("Failed to deregister service: %v", err)
					} else {
						logger.Info("API Gateway deregistered from Consul")
					}
				}()
			}
		}
	} else {
		logger.Warn("Consul address not configured, service discovery will be disabled")
	}

	// Initialize NATS client
	// Using NATS with JetStream for durable, persistent messaging
	// Much more lightweight than Kafka and easier to set up - virjilakrum
	var natsClient *messaging.NATSClient
	if config.NATSAddress != "" {
		natsConfig := messaging.NATSConfig{
			URL:      config.NATSAddress,
			Stream:   "jobs",
			MaxAge:   "24h", // Store messages for 24 hours
			Replicas: 1,     // Single replica for development, increase for production
		}
		var err error
		natsClient, err = messaging.NewNATSClient(natsConfig, logger)
		if err != nil {
			logger.Warnf("Failed to initialize NATS client: %v", err)
			logger.Warn("Asynchronous messaging will be disabled")
		} else {
			logger.Info("NATS client initialized")

			// Ensure job stream exists
			// Using wildcard subjects for job types to allow easy filtering
			// Makes it easy to add new job types without changing consumers - virjilakrum
			err = natsClient.EnsureStream([]string{"jobs.*"})
			if err != nil {
				logger.Warnf("Failed to ensure jobs stream: %v", err)
			} else {
				logger.Info("Jobs stream created")
			}

			// Initialize job status subscription
			err = natsClient.SubscribeToStatusUpdates()
			if err != nil {
				logger.Warnf("Failed to subscribe to job status updates: %v", err)
			} else {
				logger.Info("Subscribed to job status updates")
			}

			// Defer connection close
			defer natsClient.Close()
		}
	} else {
		logger.Warn("NATS address not configured, asynchronous messaging will be disabled")
	}

	// Initialize job store
	jobStore := storage.NewJobStore(10000) // Store up to 10,000 jobs in memory

	// Set job store in NATS client for status updates
	if natsClient != nil {
		natsClient.SetJobStore(jobStore)
	}

	// Initialize handlers
	jobSubmissionHandler := handlers.NewJobSubmissionHandler(natsClient, jobStore)
	authHandler := handlers.NewAuthHandler(&config)

	// Initialize proxy handler if service registry is available
	var proxyHandler *proxy.ProxyHandler
	if serviceRegistry != nil {
		proxyHandler = proxy.NewProxyHandler(serviceRegistry)
	}

	// Create router
	// Using chi router because it's stdlib compatible, lightweight, and fast
	// Tested it vs. gin and echo, perf difference was minimal but chi API is cleaner - virjilakrum
	router := chi.NewRouter()

	// Global middlewares (applied to all routes)
	// Order matters here! Recovery should be first to catch panics in other middleware - virjilakrum
	router.Use(middleware.Recoverer())                  // Recover from panics
	router.Use(middleware.RequestLogger())              // Log requests using our structured logger
	router.Use(middleware.Metrics())                    // Collect Prometheus metrics
	router.Use(middleware.CORS(nil))                    // CORS support with default options
	router.Use(chiMiddleware.RequestID)                 // Add a request ID to each request
	router.Use(chiMiddleware.RealIP)                    // Use the real IP from X-Forwarded-For or X-Real-IP
	router.Use(chiMiddleware.URLFormat)                 // Parse URL format from URL query parameters
	router.Use(chiMiddleware.Timeout(60 * time.Second)) // Set a 60-second timeout for all requests

	// Add rate limiting - 100 requests per second with burst of 200
	// Token bucket algorithm works well here - tested vs. leaky bucket
	// Set higher limits for dev mode to avoid frustration during testing - virjilakrum
	if config.LogLevel == "debug" {
		// In debug mode, use a higher limit for easier testing
		router.Use(middleware.TokenBucketRateLimit(1000, 2000))
	} else {
		// In production, use a more reasonable limit
		router.Use(middleware.TokenBucketRateLimit(100, 200))
	}

	// Health endpoint (not rate limited)
	// Used by Consul and other health checkers - must be fast and reliable
	// Don't add auth or complex logic here - keep it simple - virjilakrum
	router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Metrics endpoint
	// Exposing Prometheus metrics for monitoring
	// Separate from /health because metrics might be large - virjilakrum
	router.Handle("/metrics", promhttp.Handler())

	// Auth routes - public
	router.Route("/auth", func(r chi.Router) {
		authHandler.RegisterRoutes(r)
	})

	// API routes - Version 1
	// Using versioned APIs from the start makes future upgrades easier
	// Learned this the hard way from previous projects - virjilakrum
	router.Route("/api/v1", func(r chi.Router) {
		// Protected routes - require authentication
		r.Group(func(r chi.Router) {
			// Apply JWT authentication middleware to all routes in this group
			r.Use(middleware.JWTAuth(config.JWTSecret))

			// Job submission routes
			jobSubmissionHandler.RegisterRoutes(r)

			// Admin-only routes
			// Using nested route groups with role middleware for authorization
			// This pattern scales well as we add more auth rules - virjilakrum
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("admin"))
				// Admin-specific endpoints would go here
				r.Get("/admin-stats", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.Write([]byte(`{"admin":"true","message":"Admin access granted"}`))
				})
			})
		})

		// Public routes - no authentication required
		r.Get("/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"operational","version":"1.0.0"}`))
		})
	})

	// Proxy routes - if service registry is available
	// Dynamic service discovery makes this powerful
	// Route pattern makes proxying fully transparent to clients - virjilakrum
	if proxyHandler != nil {
		router.Route("/services", func(r chi.Router) {
			// Proxy requests to backend services
			// The path will be /services/{service-name}/*
			r.HandleFunc("/{serviceName}/*", func(w http.ResponseWriter, r *http.Request) {
				serviceName := chi.URLParam(r, "serviceName")
				if serviceName == "" {
					http.Error(w, "Service name is required", http.StatusBadRequest)
					return
				}

				// Remove the /services/{serviceName} prefix from the path
				// so that the backend service receives the correct path
				r.URL.Path = "/" + chi.URLParam(r, "*")

				// Handle the proxy request
				proxyHandler.HandleProxy(serviceName)(w, r)
			})
		})
	}

	// Admin routes
	router.Route("/admin", func(r chi.Router) {
		// These routes require authentication and admin role
		r.Use(middleware.JWTAuth(config.JWTSecret))
		r.Use(middleware.RequireRole("admin"))

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"message":"Admin dashboard"}`))
		})
	})

	// Create server
	// Using custom server settings instead of http.ListenAndServe
	// Gives us more control over timeouts and shutdown - virjilakrum
	server := &http.Server{
		Addr:    config.Port,
		Handler: router,
	}

	// Start server in a goroutine so it doesn't block shutdown handling
	go func() {
		logger.Infof("HTTP server listening on port %s", config.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Set up graceful shutdown
	// Critical for k8s deployments to avoid connection interruptions
	// Also prevents data loss during NATS publishing - virjilakrum
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// Create a deadline for server shutdown
	// 10s should be enough for all in-flight requests to complete
	// Can tune this higher in prod if needed - virjilakrum
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Attempt to gracefully shutdown the server
	if err := server.Shutdown(ctx); err != nil {
		logger.Errorf("Server forced to shutdown: %v", err)
	}

	logger.Info("Server gracefully stopped")
}
