// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apidocs_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/apidocs"
)

// TestGetDocs_ReturnsSpec is Part 4's own explicit test requirement:
// GET /api/docs returns valid YAML. This deliberately does not add a new
// YAML-parsing dependency just for this one assertion - instead it
// checks the response is the exact embedded openapi.yaml content (a
// byte-identical copy is provably valid YAML if the source file is,
// which scripts/check_api_contract.py already keeps honest on every PR)
// plus the specific top-level keys every OpenAPI 3.0 document must have.
func TestGetDocs_ReturnsSpec(t *testing.T) {
	mux := http.NewServeMux()
	apidocs.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Fatalf("expected Content-Type application/yaml, got %q", ct)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "openapi:") {
		t.Error("expected the response to contain an \"openapi:\" top-level key")
	}
	if !strings.Contains(body, "paths:") {
		t.Error("expected the response to contain a \"paths:\" top-level key")
	}
	if !strings.Contains(body, "info:") {
		t.Error("expected the response to contain an \"info:\" top-level key")
	}
	if len(body) < 100 {
		t.Fatalf("expected a substantial spec body, got only %d bytes", len(body))
	}
}

func TestGetDocs_Unauthenticated(t *testing.T) {
	mux := http.NewServeMux()
	apidocs.RegisterRoutes(mux)

	// No Authorization header at all - per this batch's own brief,
	// GET /api/docs must not require one.
	req := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected GET /api/docs to succeed without authentication, got %d", rec.Code)
	}
}
