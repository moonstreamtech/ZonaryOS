// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package config loads process configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
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

	// PublicURL is this installation's own configured public base URL
	// (no trailing slash), e.g. https://zonaryos.duckdns.org - the same
	// "what is my own public address" value docker-compose.prod.yml's
	// KC_HOSTNAME convention already establishes for Keycloak, reused
	// here rather than inventing a second one. It is what
	// internal/discovery.HandleWellKnown answers with at
	// GET /.well-known/zonaryos-central, and (via DiscoveryStartURL's
	// default, see below) what a fresh installation's own discovery
	// client starts from. Empty by default - plain local dev has no
	// real public URL, and both consumers treat an empty value as
	// "nothing to serve/start from" rather than guessing.
	PublicURL string
	// DiscoveryStartURL is the address internal/discovery.Client begins
	// resolving from (ZONARYOS_DISCOVERY_START_URL). Defaults to
	// PublicURL when unset - for today's single-instance reality
	// (Open Points item 34), an installation's own discovery starting
	// point is itself, the same self-referential answer
	// HandleWellKnown gives. A real future migration would repoint this
	// at whatever address is actually known to be further along the
	// discovery chain, without requiring any code change - see
	// internal/discovery's package doc comment.
	DiscoveryStartURL string

	// TelemetryEnabled is internal/telemetry's single default-disabled
	// switch, mirroring LicenseEnforced's exact convention: only "true"
	// (exact, case-sensitive match) turns it on. Default OFF - this
	// collects source IP addresses (aggregate request/operation counts
	// server-side, page/feature-usage counts client-side), which has
	// real privacy weight (see docs/OPEN_POINTS.md's KVKK
	// cross-reference) - an operator must explicitly opt in, never
	// discover after the fact that telemetry was silently on.
	TelemetryEnabled bool

	// NATSURL (ZONARYOS_NATS_URL, NATS JetStream/Edge Agent completion
	// batch) is internal/edgeagent.NATSBridge's single default-disabled
	// switch, mirroring LicenseEnforced/TelemetryEnabled's exact
	// convention: empty (the default) means NATSBridge is nil and the
	// system behaves exactly as before this batch - direct HTTP push to
	// POST /api/edge/events. Only when set does the server also connect
	// to this NATS server and run a durable JetStream consumer alongside
	// the existing HTTP path (both remain live at once; NATS is an
	// additional ingestion path, not a replacement for the HTTP one -
	// see internal/edgeagent/nats.go's own doc comment).
	NATSURL string
	// NATSPollInterval (ZONARYOS_NATS_POLL_INTERVAL) is how often the
	// server's durable pull consumer fetches from the JetStream stream -
	// only read when NATSURL is set. Defaults to 5s per this batch's own
	// design brief.
	NATSPollInterval time.Duration

	// RequestTimeout (ZONARYOS_REQUEST_TIMEOUT, server infrastructure
	// hardening batch) bounds how long any single request's own context
	// may run before internal/platform/middleware's timeout middleware
	// cancels it - defaults to 30s. See that middleware's own doc
	// comment for why the permission-events SSE stream is exempted from
	// this bound.
	RequestTimeout time.Duration
	// MaxJSONBodyBytes/MaxUploadBodyBytes (ZONARYOS_MAX_JSON_BODY_BYTES/
	// ZONARYOS_MAX_UPLOAD_BODY_BYTES) bound request body size -
	// internal/platform/middleware's size-limit middleware picks between
	// the two per request based on Content-Type. Default 1MB/10MB per
	// this batch's own design brief.
	MaxJSONBodyBytes   int64
	MaxUploadBodyBytes int64
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
		PublicURL:           strings.TrimRight(os.Getenv("ZONARYOS_PUBLIC_URL"), "/"),
		TelemetryEnabled:    os.Getenv("ZONARYOS_TELEMETRY_ENABLED") == "true",
		NATSURL:             os.Getenv("ZONARYOS_NATS_URL"),
		NATSPollInterval:    5 * time.Second,
		RequestTimeout:      30 * time.Second,
		MaxJSONBodyBytes:    1 << 20,  // 1MB
		MaxUploadBodyBytes:  10 << 20, // 10MB
	}
	cfg.DiscoveryStartURL = strings.TrimRight(getEnv("ZONARYOS_DISCOVERY_START_URL", cfg.PublicURL), "/")

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

	if raw := os.Getenv("ZONARYOS_NATS_POLL_INTERVAL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("ZONARYOS_NATS_POLL_INTERVAL must be a positive Go duration (e.g. \"5s\")")
		}
		cfg.NATSPollInterval = d
	}
	if raw := os.Getenv("ZONARYOS_REQUEST_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("ZONARYOS_REQUEST_TIMEOUT must be a positive Go duration (e.g. \"30s\")")
		}
		cfg.RequestTimeout = d
	}
	if raw := os.Getenv("ZONARYOS_MAX_JSON_BODY_BYTES"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("ZONARYOS_MAX_JSON_BODY_BYTES must be a positive integer")
		}
		cfg.MaxJSONBodyBytes = n
	}
	if raw := os.Getenv("ZONARYOS_MAX_UPLOAD_BODY_BYTES"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("ZONARYOS_MAX_UPLOAD_BODY_BYTES must be a positive integer")
		}
		cfg.MaxUploadBodyBytes = n
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
