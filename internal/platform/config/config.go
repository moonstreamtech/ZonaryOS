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
	"time"
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

	// LicenseEnforced is internal/license's single default-disabled
	// switch (Open Points item 32). Only "true" (exact, case-sensitive
	// match) turns it on - unset or any other value leaves it false, so
	// every code path in this system behaves exactly as it did before
	// this package existed unless an operator explicitly opts in. See
	// internal/license.Verifier's doc comment for the structural
	// guarantee this flag's value ultimately gates.
	LicenseEnforced bool
	// LicensePublicKey is the Ed25519 public key (base64url or base64)
	// used to verify a license token's signature - see
	// internal/license's package doc comment for why this codebase only
	// ever holds a public key, never a private one. Only required (and
	// only ever read) when LicenseEnforced is true.
	LicensePublicKey string
	// LicenseToken is the signed token string itself (see
	// internal/license's "Token wire format" doc comment) - normally
	// issued by cmd/licensegen for test/dev, or by a real central
	// license server once Open Points item 34 designs one. Only
	// required (and only ever read) when LicenseEnforced is true.
	LicenseToken string
	// LicenseGracePeriod is how long past a token's expiry
	// internal/license.Verifier.Allow keeps allowing access before
	// failing closed - see that package's DefaultGracePeriod constant
	// for why 72h is this codebase's current PROVISIONAL default
	// (Open Points item 32's open question 2 is still unresolved).
	// Zero means "use the package default", not "no grace period" -
	// there is no env-var-level way to request a zero grace period,
	// matching the brief's ask for a sensible provisional default
	// rather than a fully general knob.
	LicenseGracePeriod time.Duration
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
		LicenseEnforced:     os.Getenv("ZONARYOS_LICENSE_ENFORCEMENT") == "true",
		LicensePublicKey:    os.Getenv("ZONARYOS_LICENSE_PUBLIC_KEY"),
		LicenseToken:        os.Getenv("ZONARYOS_LICENSE_TOKEN"),
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

	// Only parsed if set at all - an unset ZONARYOS_LICENSE_GRACE_PERIOD
	// leaves LicenseGracePeriod at its zero value, which
	// internal/license.NewVerifier treats as "use DefaultGracePeriod",
	// not "no grace period" (see that field's doc comment above). A
	// value that IS set but doesn't parse as a Go duration is a real
	// config error worth failing startup over, same as this function's
	// other required-field checks.
	if raw := os.Getenv("ZONARYOS_LICENSE_GRACE_PERIOD"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("ZONARYOS_LICENSE_GRACE_PERIOD must be a valid Go duration (e.g. \"72h\"): %w", err)
		}
		cfg.LicenseGracePeriod = d
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
