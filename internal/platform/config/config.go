// Package config loads process configuration from environment variables.
package config

import (
	"fmt"
	"os"
)

// Config holds the runtime configuration for the ZonaryOS server binary.
type Config struct {
	HTTPAddr string
}

// Load reads configuration from environment variables, applying defaults
// where a variable is not set.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr: getEnv("ZONARYOS_HTTP_ADDR", ":8080"),
	}

	if cfg.HTTPAddr == "" {
		return Config{}, fmt.Errorf("ZONARYOS_HTTP_ADDR must not be empty")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
