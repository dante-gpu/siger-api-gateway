package internal

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v2"
)

// Config represents the application configuration
// Using struct tags to map YAML fields - much cleaner than manual mapping
// Had to add omitempty to handle optional fields gracefully - virjilakrum
type Config struct {
	Port          string `yaml:"port"`
	LogLevel      string `yaml:"logLevel"`
	JWTSecret     string `yaml:"jwtSecret"`
	JWTExpiration int    `yaml:"jwtExpiration"` // JWT token expiration in minutes
	ConsulAddress string `yaml:"consulAddress"`
	NATSAddress   string `yaml:"natsAddress"`
	CORSAllowed   struct {
		Origins []string `yaml:"origins"`
		Methods []string `yaml:"methods"`
		Headers []string `yaml:"headers"`
	} `yaml:"corsAllowed,omitempty"`
}

// DefaultConfig provides default configuration values
// Started with more restrictive defaults, but it caused too many issues
// These are safer defaults for getting started quickly - virjilakrum
func DefaultConfig() Config {
	config := Config{
		Port:          ":8080",
		LogLevel:      "info",
		JWTSecret:     "default-jwt-secret-change-me-in-production", // Obviously needs to be changed in prod
		JWTExpiration: 60,                                           // Default 60 minutes (1 hour) expiration
		ConsulAddress: "localhost:8500",
		NATSAddress:   "nats://localhost:4222",
	}

	// Default CORS settings - initially had * for origins but that's too permissive
	// These are safer defaults that still work for most dev environments - virjilakrum
	config.CORSAllowed.Origins = []string{"http://localhost:3000", "http://localhost:8080"}
	config.CORSAllowed.Methods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"}
	config.CORSAllowed.Headers = []string{
		"Accept", "Authorization", "Content-Type", "X-CSRF-Token",
		"X-Request-ID", "X-Requested-With",
	}

	return config
}

// LoadConfig loads the configuration from a file
// Supports YAML format with environment variable substitution
// Could add JSON support but YAML is more human-readable - virjilakrum
func LoadConfig(configPath string) (Config, error) {
	config := DefaultConfig()

	// Determine the config file path
	// Tried using viper initially, but it was overkill for our needs - virjilakrum
	configFile := filepath.Join(configPath, "config.yaml")

	// Read the file
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return config, fmt.Errorf("reading config file: %w", err)
	}

	// Substitute environment variables
	// This gives us 12-factor app compatibility without a heavy library
	// Format is ${ENV_VAR} or ${ENV_VAR:default_value} - virjilakrum
	expandedData := os.ExpandEnv(string(data))

	// Unmarshal YAML
	err = yaml.Unmarshal([]byte(expandedData), &config)
	if err != nil {
		return config, fmt.Errorf("parsing config file: %w", err)
	}

	// Validate config
	// Simple validation for now, might add more comprehensive schema validation later - virjilakrum
	if config.Port == "" {
		config.Port = ":8080" // Default port
	} else if !strings.HasPrefix(config.Port, ":") {
		// Ensure port starts with :
		config.Port = ":" + config.Port
	}

	// Ensure log level is valid
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
		"fatal": true,
	}

	if !validLogLevels[strings.ToLower(config.LogLevel)] {
		config.LogLevel = "info" // Default to info if invalid
	}

	return config, nil
}

// EnsureConfigExists creates a default config file if one doesn't exist
// This saves a ton of time during setup - no more "config file not found" errors
// Had too many support requests about this before adding this function - virjilakrum
func EnsureConfigExists(configPath string) error {
	// Ensure directory exists
	err := os.MkdirAll(configPath, 0755)
	if err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	// Check if config file exists
	configFile := filepath.Join(configPath, "config.yaml")
	_, err = os.Stat(configFile)
	if os.IsNotExist(err) {
		// Config doesn't exist, create with template
		// Using a template string instead of marshaling the config
		// The template provides better comments and formatting - virjilakrum

		// Add helpful comments to the default config
		// YAML comments make the config file more self-documenting - virjilakrum
		commentedData :=
			`# API Gateway Configuration
# Generated automatically - modify as needed
# Environment variables can be used with ${ENV_VAR} syntax
# Example: jwtSecret: ${JWT_SECRET:default-secret}

port: :8080          # The port to listen on
logLevel: info       # debug, info, warn, error, or fatal
jwtSecret: default-jwt-secret-change-me-in-production  # Secret for JWT signing - CHANGE THIS!
jwtExpiration: 60  # JWT token expiration in minutes

# Service discovery configuration
consulAddress: localhost:8500   # Consul address for service discovery

# Messaging configuration
natsAddress: nats://localhost:4222  # NATS address for async messaging

# CORS configuration
corsAllowed:
  origins:
    - http://localhost:3000
    - http://localhost:8080
  methods:
    - GET
    - POST
    - PUT
    - DELETE
    - OPTIONS
    - PATCH
  headers:
    - Accept
    - Authorization
    - Content-Type
    - X-CSRF-Token
    - X-Request-ID
    - X-Requested-With
`

		// Write the commented config to file
		err = ioutil.WriteFile(configFile, []byte(commentedData), 0644)
		if err != nil {
			return fmt.Errorf("writing default config: %w", err)
		}

		fmt.Println("Created default configuration file")
	} else if err != nil {
		return fmt.Errorf("checking config file: %w", err)
	}

	return nil
}
