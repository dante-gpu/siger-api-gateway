package internal

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
)

type Config struct {
	Port          string `mapstructure:"port"`
	ConsulAddress string `mapstructure:"consul_address"`
	NATSAddress   string `mapstructure:"nats_address"`
	LogLevel      string `mapstructure:"log_level"`

	// Authentication settings
	JWTSecret     string `mapstructure:"jwt_secret"`
	JWTExpiration int    `mapstructure:"jwt_expiration"` // In minutes
}

func LoadConfig(path string) (config Config, err error) {
	viper.AddConfigPath(path)
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	viper.AutomaticEnv()

	err = viper.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			fmt.Println("Config file not found; using defaults")
		} else {
			return Config{}, fmt.Errorf("failed to read config: %w", err)
		}
	}

	err = viper.Unmarshal(&config)
	if err != nil {
		return Config{}, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Set default values if not provided
	if config.LogLevel == "" {
		config.LogLevel = "info"
	}

	if config.JWTSecret == "" {
		config.JWTSecret = "default-very-secure-jwt-secret-key-change-in-production"
	}

	if config.JWTExpiration == 0 {
		config.JWTExpiration = 60 // 60 minutes default
	}

	return
}

func EnsureConfigExists(path string) error {
	configPath := path + "/config.yaml"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Printf("Config file not found, creating default config at %s\n", configPath)
		// Create a default config file
		defaultConfig := []byte(`port: ":8080"
consul_address: "localhost:8500"
nats_address: "localhost:4222"
log_level: "info"
jwt_secret: "default-very-secure-jwt-secret-key-change-in-production"
jwt_expiration: 60`)

		err = os.WriteFile(configPath, defaultConfig, 0644)
		if err != nil {
			return fmt.Errorf("failed to create default config: %w", err)
		}
	}
	return nil
}
