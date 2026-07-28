// Package config loads process configuration from environment variables.
package config

import (
	"fmt"
	"os"
)

// Config holds the runtime configuration for the ZonaryOS server binary.
type Config struct {
	HTTPAddr string

	// DatabaseURL must reference the unprivileged zonaryos_app role - see
	// internal/platform/db and migrations/0001_core_schema.up.sql.
	DatabaseURL string

	// OIDCIssuerURL is the ZonaryOS Keycloak realm's issuer URL (e.g.
	// http://localhost:8081/realms/zonaryos), used to discover its JWKS
	// endpoint for bearer token verification.
	OIDCIssuerURL string
	// OIDCClientID is the expected "azp" (authorized party) claim on
	// verified tokens - see internal/identity.Verifier.
	OIDCClientID string
}

// Load reads configuration from environment variables, applying defaults
// where a variable is not set.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:      getEnv("ZONARYOS_HTTP_ADDR", ":8080"),
		DatabaseURL:   os.Getenv("ZONARYOS_DATABASE_URL"),
		OIDCIssuerURL: os.Getenv("ZONARYOS_OIDC_ISSUER_URL"),
		OIDCClientID:  getEnv("ZONARYOS_OIDC_CLIENT_ID", "zonaryos-web"),
	}

	if cfg.HTTPAddr == "" {
		return Config{}, fmt.Errorf("ZONARYOS_HTTP_ADDR must not be empty")
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("ZONARYOS_DATABASE_URL must be set")
	}
	if cfg.OIDCIssuerURL == "" {
		return Config{}, fmt.Errorf("ZONARYOS_OIDC_ISSUER_URL must be set")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
