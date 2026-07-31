// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package config loads process configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strings"
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

	// PlatformAdminEmails is the hardcoded allowlist of ZonaryOS-the-
	// company staff permitted to use internal/platformadmin's read-only,
	// cross-firm metadata view (see that package's doc comment). This is
	// option (a) from that package's design decision - a plain allowlist
	// checked against the existing Keycloak-authenticated identity's
	// email, not a new Keycloak realm/client - so it belongs here rather
	// than a new config mechanism, following this file's existing
	// env-var convention. Empty by default: nobody is a platform admin
	// until this is explicitly set, matching the "deny by default"
	// posture the rest of this system already follows.
	PlatformAdminEmails []string
}

// Load reads configuration from environment variables, applying defaults
// where a variable is not set.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:            getEnv("ZONARYOS_HTTP_ADDR", ":8080"),
		DatabaseURL:         os.Getenv("ZONARYOS_DATABASE_URL"),
		OIDCIssuerURL:       os.Getenv("ZONARYOS_OIDC_ISSUER_URL"),
		OIDCClientID:        getEnv("ZONARYOS_OIDC_CLIENT_ID", "zonaryos-web"),
		PlatformAdminEmails: parseEmailList(os.Getenv("ZONARYOS_PLATFORM_ADMIN_EMAILS")),
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

// parseEmailList splits a comma-separated env var into a trimmed,
// lower-cased, non-empty slice - internal/platformadmin.NewAllowlist does
// its own normalization too, but normalizing here as well means a value
// logged or inspected straight off Config already reads the way it will be
// compared.
func parseEmailList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	emails := make([]string, 0, len(parts))
	for _, p := range parts {
		e := strings.ToLower(strings.TrimSpace(p))
		if e == "" {
			continue
		}
		emails = append(emails, e)
	}
	return emails
}
