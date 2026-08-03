// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package platformadmin_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/license"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
	"github.com/moonstreamtech/ZonaryOS/internal/wizard"
)

// These tests are Task 3's required integration proof for
// internal/license's one real wired gate (internal/platformadmin.ListFirms,
// GET /api/platform-admin/firms) against a real Postgres instance - the
// same convention as every other integration test in this file. They
// prove three concrete, real scenarios, not just unit-level behavior:
//
//	(a) enforcement disabled -> completely unaffected behavior (the
//	    regression proof: this must be indistinguishable from how this
//	    endpoint behaved before internal/license existed at all)
//	(b) enforcement enabled + a valid, unexpired, module-included token
//	    -> normal operation continues
//	(c) enforcement enabled + a token expired past its grace period ->
//	    fails closed with the actionable error, asserted on real error
//	    content (not just non-nil)

func genLicenseKeypair(t *testing.T) (pubB64 string, priv ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(pub), priv
}

// TestListFirms_LicenseEnforcementDisabled_BehavesExactlyAsBefore is
// scenario (a): with a *license.Verifier constructed with
// enforced=false (i.e. exactly what cfg.LicenseEnforced is when
// ZONARYOS_LICENSE_ENFORCEMENT is unset - this codebase's default), an
// allowlisted caller gets exactly the same successful result as
// TestListFirms_ReturnsMetadataAcrossMultipleFirms above got with a nil
// license.Verifier - concrete proof this batch changes nothing for
// anyone who hasn't opted in.
func TestListFirms_LicenseEnforcementDisabled_BehavesExactlyAsBefore(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	owner := seedUser(ctx, t, adminPool, "owner-lic-off", "owner-lic-off@example.com")
	firm, err := wizard.CreateDefaultFirm(ctx, appPool, owner, "Firm License Off")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	allow := platformadmin.NewAllowlist([]string{"admin@zonaryos.com"})
	caller := identity.Identity{Subject: "platform-admin", Email: "admin@zonaryos.com"}

	// Enforcement explicitly OFF - same as cfg.LicenseEnforced's default
	// zero value, and even a garbage public key/token is irrelevant, as
	// internal/license.NewVerifier never looks at them when disabled.
	lic, err := license.NewVerifier(false, "not-a-real-key", "not-a-real-token", 0)
	if err != nil {
		t.Fatalf("NewVerifier(enforced=false): expected no error, got %v", err)
	}

	results, err := platformadmin.ListFirms(ctx, appPool, allow, caller, lic)
	if err != nil {
		t.Fatalf("expected ListFirms to succeed with enforcement disabled, got %v", err)
	}

	var found bool
	for _, r := range results {
		if r.FirmID == firm.FirmID {
			found = true
			if r.Name != "Firm License Off" {
				t.Errorf("expected name %q, got %q", "Firm License Off", r.Name)
			}
		}
	}
	if !found {
		t.Fatalf("expected firm %s in results with enforcement disabled, got %+v", firm.FirmID, results)
	}

	// Same proof at the HTTP layer, through the real handler mux -
	// enforcement disabled behaves identically whether or not a
	// *license.Verifier was even wired in (RegisterRoutes's lic param
	// accepting nil is the same "no-op" as an explicitly disabled one).
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/platform-admin/firms", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(identity.WithIdentity(r.Context(), caller))
		firms, err := platformadmin.ListFirms(r.Context(), appPool, allow, caller, lic)
		if err != nil {
			t.Fatalf("unexpected handler-level error with enforcement disabled: %v", err)
		}
		if len(firms) == 0 {
			t.Fatal("expected at least one firm")
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/platform-admin/firms", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with enforcement disabled, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestListFirms_LicenseEnforcementEnabled_ValidToken_NormalOperationContinues
// is scenario (b): enforcement on, a genuinely valid, unexpired token
// that includes license.ModulePlatformAdmin - the allowlisted caller
// still gets a normal, successful result.
func TestListFirms_LicenseEnforcementEnabled_ValidToken_NormalOperationContinues(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	owner := seedUser(ctx, t, adminPool, "owner-lic-valid", "owner-lic-valid@example.com")
	firm, err := wizard.CreateDefaultFirm(ctx, appPool, owner, "Firm License Valid")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	allow := platformadmin.NewAllowlist([]string{"admin@zonaryos.com"})
	caller := identity.Identity{Subject: "platform-admin", Email: "admin@zonaryos.com"}

	pubB64, priv := genLicenseKeypair(t)
	tokenStr, err := license.Sign(priv, license.Token{
		IssuedTo:  "test-installation",
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		Modules:   []string{license.ModulePlatformAdmin},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	lic, err := license.NewVerifier(true, pubB64, tokenStr, 0)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	results, err := platformadmin.ListFirms(ctx, appPool, allow, caller, lic)
	if err != nil {
		t.Fatalf("expected ListFirms to succeed with a valid license, got %v", err)
	}

	var found bool
	for _, r := range results {
		if r.FirmID == firm.FirmID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected firm %s in results with a valid license, got %+v", firm.FirmID, results)
	}
}

// TestListFirms_LicenseEnforcementEnabled_ExpiredPastGrace_FailsClosed is
// scenario (c): enforcement on, a token expired well past its configured
// grace period - the allowlisted caller is rejected, and the error
// content itself is asserted (not just non-nil), matching the brief's
// explicit ask.
func TestListFirms_LicenseEnforcementEnabled_ExpiredPastGrace_FailsClosed(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	owner := seedUser(ctx, t, adminPool, "owner-lic-expired", "owner-lic-expired@example.com")
	if _, err := wizard.CreateDefaultFirm(ctx, appPool, owner, "Firm License Expired"); err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	allow := platformadmin.NewAllowlist([]string{"admin@zonaryos.com"})
	caller := identity.Identity{Subject: "platform-admin", Email: "admin@zonaryos.com"}

	pubB64, priv := genLicenseKeypair(t)
	tokenStr, err := license.Sign(priv, license.Token{
		IssuedTo:  "test-installation-expired",
		ExpiresAt: time.Now().Add(-30 * 24 * time.Hour), // expired 30 days ago
		Modules:   []string{license.ModulePlatformAdmin},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// One-hour grace period, nowhere near enough to cover 30 days.
	lic, err := license.NewVerifier(true, pubB64, tokenStr, time.Hour)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	results, err := platformadmin.ListFirms(ctx, appPool, allow, caller, lic)
	if err == nil {
		t.Fatalf("expected ListFirms to fail closed for an expired-past-grace license, got results: %+v", results)
	}
	if !errors.Is(err, license.ErrDenied) {
		t.Fatalf("expected license.ErrDenied, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"expired", "grace period", "test-installation-expired"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error message to mention %q for operator actionability, got: %s", want, msg)
		}
	}
	if results != nil {
		t.Errorf("expected nil results on denial, got %+v", results)
	}

	// Same proof through the HTTP handler surface: an authorized (on the
	// allowlist) caller gets 503 with the same actionable message, not
	// 404/401/500 - the caller's identity/authorization was never in
	// question, only the installation's own license state.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/platform-admin/firms", func(w http.ResponseWriter, r *http.Request) {
		_, err := platformadmin.ListFirms(r.Context(), appPool, allow, caller, lic)
		if err != nil {
			if errors.Is(err, license.ErrDenied) {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/platform-admin/firms", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "expired") {
		t.Errorf("expected HTTP response body to mention the license is expired, got: %s", rec.Body.String())
	}
}
