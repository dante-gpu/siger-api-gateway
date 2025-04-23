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
)

func main() {
	fmt.Println("Starting API Gateway...")

	// Ensure config file exists
	configPath := "configs"
	err := internal.EnsureConfigExists(configPath)
	if err != nil {
		log.Fatal("cannot ensure config exists:", err)
	}

	// Load config
	config, err := internal.LoadConfig(configPath)
	if err != nil {
		log.Fatal("cannot load config:", err)
	}

	// Initialize logger
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
	serviceRegistry, err := discovery.NewServiceRegistry(config.ConsulAddress)
	if err != nil {
		logger.Warnf("Failed to initialize service registry: %v", err)
		logger.Warn("Service discovery will be disabled")
	} else {
		logger.Info("Service registry initialized")

		// Register the API Gateway itself
		// Generate a unique ID using hostname and current timestamp
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

	// Initialize NATS client
	natsClient, err := messaging.NewNATSClient(config.NATSAddress)
	if err != nil {
		logger.Warnf("Failed to initialize NATS client: %v", err)
		logger.Warn("Asynchronous messaging will be disabled")
	} else {
		logger.Info("NATS client initialized")

		// Create job streams
		err = natsClient.CreateStream("jobs", []string{"jobs.*"})
		if err != nil {
			logger.Warnf("Failed to create jobs stream: %v", err)
		} else {
			logger.Info("Jobs stream created")
		}

		// Defer connection close
		defer natsClient.Close()
	}

	// Initialize handlers
	jobSubmissionHandler := handlers.NewJobSubmissionHandler(natsClient)
	authHandler := handlers.NewAuthHandler(&config)

	// Initialize proxy handler if service registry is available
	var proxyHandler *proxy.ProxyHandler
	if serviceRegistry != nil {
		proxyHandler = proxy.NewProxyHandler(serviceRegistry)
	}

	// Create router
	router := chi.NewRouter()

	// Global middlewares (applied to all routes)
	router.Use(middleware.Recoverer())                  // Recover from panics
	router.Use(middleware.RequestLogger())              // Log requests using our structured logger
	router.Use(middleware.Metrics())                    // Collect Prometheus metrics
	router.Use(middleware.CORS(nil))                    // CORS support with default options
	router.Use(chiMiddleware.RequestID)                 // Add a request ID to each request
	router.Use(chiMiddleware.RealIP)                    // Use the real IP from X-Forwarded-For or X-Real-IP
	router.Use(chiMiddleware.URLFormat)                 // Parse URL format from URL query parameters
	router.Use(chiMiddleware.Timeout(60 * time.Second)) // Set a 60-second timeout for all requests

	// Add rate limiting - 100 requests per second with burst of 200
	if config.LogLevel == "debug" {
		// In debug mode, use a higher limit for easier testing
		router.Use(middleware.TokenBucketRateLimit(1000, 2000))
	} else {
		// In production, use a more reasonable limit
		router.Use(middleware.TokenBucketRateLimit(100, 200))
	}

	// Health endpoint (not rate limited)
	router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Metrics endpoint
	router.Handle("/metrics", promhttp.Handler())

	// Auth routes - public
	router.Route("/auth", func(r chi.Router) {
		authHandler.RegisterRoutes(r)
	})

	// API routes - Version 1
	router.Route("/api/v1", func(r chi.Router) {
		// Protected routes - require authentication
		r.Group(func(r chi.Router) {
			// Apply JWT authentication middleware to all routes in this group
			r.Use(middleware.JWTAuth(config.JWTSecret))

			// Job submission routes
			jobSubmissionHandler.RegisterRoutes(r)

			// Admin-only routes
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
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// Create a deadline for server shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Attempt to gracefully shutdown the server
	if err := server.Shutdown(ctx); err != nil {
		logger.Errorf("Server forced to shutdown: %v", err)
	}

	logger.Info("Server gracefully stopped")
}
