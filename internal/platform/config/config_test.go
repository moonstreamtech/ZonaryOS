package config

import "testing"

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("expected default HTTPAddr ':8080', got %q", cfg.HTTPAddr)
	}
}

func TestLoad_FromEnv(t *testing.T) {
	t.Setenv("ZONARYOS_HTTP_ADDR", ":9090")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("expected HTTPAddr ':9090', got %q", cfg.HTTPAddr)
	}
}

func TestLoad_RejectsEmptyAddr(t *testing.T) {
	t.Setenv("ZONARYOS_HTTP_ADDR", "")

	if _, err := Load(); err == nil {
		t.Error("expected error for empty ZONARYOS_HTTP_ADDR, got nil")
	}
}
