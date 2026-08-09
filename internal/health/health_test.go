// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandler_KeycloakUnreachableReportsDegraded confirms that when
// Keycloak's discovery document can't be reached at all (here, a
// discoveryURL pointing at nothing listening), the keycloak check reports
// "error" while status degrades rather than crashing - the "some checks
// pass, some don't" branch. pool is nil in every test here (a real
// Postgres round trip is exercised by the integration test suite
// elsewhere in this repo, not this package's own unit tests) so the
// database check is always "error" - see checkDatabase's own nil-pool
// guard - meaning every scenario below is exercised through the "error"/
// "degraded" branches only; a true "ok" verdict needs both a live pool
// and a live discovery endpoint, out of scope for a dependency-free unit
// test.
func TestHandler_KeycloakUnreachableReportsDegraded(t *testing.T) {
	// Nothing listens on this address - the request fails outright
	// rather than timing out, keeping the test fast.
	h := Handler(nil, "http://127.0.0.1:1/.well-known/openid-configuration", http.DefaultClient)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no dependency is reachable, got %d", rec.Code)
	}

	var got response
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status != OverallError {
		t.Errorf("expected overall status %q with both dependencies down, got %q", OverallError, got.Status)
	}
	if got.Checks["database"] != StatusError {
		t.Errorf("expected database check %q with a nil pool, got %q", StatusError, got.Checks["database"])
	}
	if got.Checks["keycloak"] != StatusError {
		t.Errorf("expected keycloak check %q against an unreachable discovery URL, got %q", StatusError, got.Checks["keycloak"])
	}
}

// TestHandler_KeycloakUpDatabaseDownReportsDegraded exercises the mixed
// case explicitly (database down, keycloak up) using a real
// httptest.Server standing in for Keycloak's discovery endpoint - the
// "some pass, some don't" branch producing "degraded", distinct from the
// all-down "error" case above.
func TestHandler_KeycloakUpDatabaseDownReportsDegraded(t *testing.T) {
	keycloak := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"issuer":"https://example.test"}`))
	}))
	defer keycloak.Close()

	h := Handler(nil, keycloak.URL+"/.well-known/openid-configuration", keycloak.Client())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 while degraded, got %d", rec.Code)
	}

	var got response
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Status != OverallDegraded {
		t.Errorf("expected overall status %q, got %q", OverallDegraded, got.Status)
	}
	if got.Checks["database"] != StatusError {
		t.Errorf("expected database check %q with a nil pool, got %q", StatusError, got.Checks["database"])
	}
	if got.Checks["keycloak"] != StatusOK {
		t.Errorf("expected keycloak check %q against a reachable discovery URL, got %q", StatusOK, got.Checks["keycloak"])
	}
	if got.Version == "" {
		t.Error("expected a non-empty version string")
	}
}
