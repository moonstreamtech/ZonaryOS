package config

import "testing"

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ZONARYOS_DATABASE_URL", "postgres://zonaryos_app:zonaryos_app@localhost:5432/zonaryos?sslmode=disable")
	t.Setenv("ZONARYOS_OIDC_ISSUER_URL", "http://localhost:8081/realms/zonaryos")
}

func TestLoad_Defaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("expected default HTTPAddr ':8080', got %q", cfg.HTTPAddr)
	}
	if cfg.OIDCClientID != "zonaryos-web" {
		t.Errorf("expected default OIDCClientID 'zonaryos-web', got %q", cfg.OIDCClientID)
	}
}

func TestLoad_FromEnv(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ZONARYOS_HTTP_ADDR", ":9090")
	t.Setenv("ZONARYOS_OIDC_CLIENT_ID", "custom-client")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("expected HTTPAddr ':9090', got %q", cfg.HTTPAddr)
	}
	if cfg.OIDCClientID != "custom-client" {
		t.Errorf("expected OIDCClientID 'custom-client', got %q", cfg.OIDCClientID)
	}
}

func TestLoad_RejectsEmptyAddr(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ZONARYOS_HTTP_ADDR", "")

	if _, err := Load(); err == nil {
		t.Error("expected error for empty ZONARYOS_HTTP_ADDR, got nil")
	}
}

func TestLoad_RejectsMissingDatabaseURL(t *testing.T) {
	t.Setenv("ZONARYOS_OIDC_ISSUER_URL", "http://localhost:8081/realms/zonaryos")

	if _, err := Load(); err == nil {
		t.Error("expected error for missing ZONARYOS_DATABASE_URL, got nil")
	}
}

func TestLoad_RejectsMissingOIDCIssuerURL(t *testing.T) {
	t.Setenv("ZONARYOS_DATABASE_URL", "postgres://zonaryos_app:zonaryos_app@localhost:5432/zonaryos?sslmode=disable")

	if _, err := Load(); err == nil {
		t.Error("expected error for missing ZONARYOS_OIDC_ISSUER_URL, got nil")
	}
}
