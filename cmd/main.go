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

	// Create job submission handler
	jobSubmissionHandler := handlers.NewJobSubmissionHandler(natsClient)

	// Create router
	router := chi.NewRouter()

	// Middlewares
	router.Use(middleware.Recoverer())                  // Recover from panics
	router.Use(middleware.RequestLogger())              // Log requests using our structured logger
	router.Use(middleware.Metrics())                    // Collect Prometheus metrics
	router.Use(chiMiddleware.RequestID)                 // Add a request ID to each request
	router.Use(chiMiddleware.RealIP)                    // Use the real IP from X-Forwarded-For or X-Real-IP
	router.Use(chiMiddleware.URLFormat)                 // Parse URL format from URL query parameters
	router.Use(chiMiddleware.Timeout(60 * time.Second)) // Set a 60-second timeout for all requests

	// Health endpoint
	router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Metrics endpoint
	router.Handle("/metrics", promhttp.Handler())

	// API routes - Version 1
	router.Route("/api/v1", func(r chi.Router) {
		// Register job submission routes
		jobSubmissionHandler.RegisterRoutes(r)

		// Status endpoint
		r.Get("/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"operational","version":"1.0.0"}`))
		})
	})

	// Admin routes
	router.Route("/admin", func(r chi.Router) {
		// TODO: Add admin routes and handlers
		// These would typically require authentication and authorization
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

	// Add additional shutdown logic here (e.g., close connections to NATS, Consul, etc.)

	logger.Info("Server gracefully stopped")
}
