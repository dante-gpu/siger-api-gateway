package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"siger-api-gateway/internal"
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

	router := chi.NewRouter()

	// Middlewares
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)

	// Health endpoint
	router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Start server
	fmt.Printf("Listening on port %s\n", config.Port)
	err = http.ListenAndServe(config.Port, router)
	if err != nil {
		fmt.Printf("Error starting server: %s\n", err)
	}
}
