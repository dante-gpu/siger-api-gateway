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
		// TODO: Add API routes and handlers
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
