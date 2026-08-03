// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package license_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/license"
)

func mustKeypairVerifier(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	return pub, priv
}

// TestAllow_NilVerifier_NeverDenies proves the structural default-
// disabled guarantee holds even for a nil *Verifier - the zero value of
// what a package that never got around to constructing one would have.
func TestAllow_NilVerifier_NeverDenies(t *testing.T) {
	var v *license.Verifier
	if err := v.Allow("anything"); err != nil {
		t.Fatalf("expected nil *Verifier.Allow to always return nil, got %v", err)
	}
	if !v.IsModuleActive("anything") {
		t.Fatalf("expected nil *Verifier.IsModuleActive to always return true")
	}
}

// TestAllow_DisabledEnforcement_AlwaysAllows is the core regression proof
// for this package's single most important property: with
// enforced=false, Allow never denies, regardless of module key, missing
// public key, missing token, or anything else - because NewVerifier(false, ...)
// never even looks at those parameters.
func TestAllow_DisabledEnforcement_AlwaysAllows(t *testing.T) {
	v, err := license.NewVerifier(false, "not-a-valid-key-at-all", "not-a-valid-token-either", 0)
	if err != nil {
		t.Fatalf("NewVerifier(enforced=false, ...): expected no error even with garbage config, got %v", err)
	}
	for _, moduleKey := range []string{"", "anything", license.ModulePlatformAdmin, "nonexistent_module"} {
		if err := v.Allow(moduleKey); err != nil {
			t.Errorf("Allow(%q) with enforcement disabled: expected nil, got %v", moduleKey, err)
		}
		if !v.IsModuleActive(moduleKey) {
			t.Errorf("IsModuleActive(%q) with enforcement disabled: expected true", moduleKey)
		}
	}
}

func TestNewVerifier_EnforcedWithInvalidPublicKey_Errors(t *testing.T) {
	if _, err := license.NewVerifier(true, "not-a-valid-ed25519-key", "irrelevant", 0); err == nil {
		t.Fatal("expected an error constructing an enforced Verifier with an invalid public key")
	}
}

func TestAllow_EnforcedWithNoToken_FailsClosedWithActionableMessage(t *testing.T) {
	pub, _ := mustKeypairVerifier(t)
	pubStr := base64.RawURLEncoding.EncodeToString(pub)

	v, err := license.NewVerifier(true, pubStr, "", 0)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	err = v.Allow(license.ModulePlatformAdmin)
	if !errors.Is(err, license.ErrDenied) {
		t.Fatalf("expected ErrDenied, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"ZONARYOS_LICENSE_TOKEN", "ZONARYOS_LICENSE_ENFORCEMENT", "cmd/licensegen"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error message to mention %q for operator actionability, got: %s", want, msg)
		}
	}
}

// TestAllow_ValidToken_ModuleActive_Succeeds is unit/integration
// requirement: enforcement enabled + a genuinely valid, unexpired token
// whose module list includes the requested module allows access.
func TestAllow_ValidToken_ModuleActive_Succeeds(t *testing.T) {
	pub, priv := mustKeypairVerifier(t)
	pubStr := base64.RawURLEncoding.EncodeToString(pub)

	tokenStr, err := license.Sign(priv, license.Token{
		IssuedTo:  "firm-acme",
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		Modules:   []string{license.ModulePlatformAdmin},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	v, err := license.NewVerifier(true, pubStr, tokenStr, 0)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if err := v.Allow(license.ModulePlatformAdmin); err != nil {
		t.Fatalf("expected Allow to succeed for a valid, unexpired, module-included token, got %v", err)
	}
}

func TestAllow_ValidToken_ModuleNotIncluded_Denies(t *testing.T) {
	pub, priv := mustKeypairVerifier(t)
	pubStr := base64.RawURLEncoding.EncodeToString(pub)

	tokenStr, err := license.Sign(priv, license.Token{
		IssuedTo:  "firm-acme",
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		Modules:   []string{license.ModuleAuditTrail}, // deliberately not platform_admin
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	v, err := license.NewVerifier(true, pubStr, tokenStr, 0)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	err = v.Allow(license.ModulePlatformAdmin)
	if !errors.Is(err, license.ErrDenied) {
		t.Fatalf("expected ErrDenied for a module not on the license, got %v", err)
	}
}

// TestAllow_ExpiredPastGracePeriod_FailsClosedWithActionableMessage is
// integration requirement (c): a token expired further in the past than
// the configured grace period fails closed, with a message that actually
// names what's wrong (expiry) and what to do (renew / set the env var),
// asserted on real error content, not just non-nil.
func TestAllow_ExpiredPastGracePeriod_FailsClosedWithActionableMessage(t *testing.T) {
	pub, priv := mustKeypairVerifier(t)
	pubStr := base64.RawURLEncoding.EncodeToString(pub)

	tokenStr, err := license.Sign(priv, license.Token{
		IssuedTo:  "firm-acme",
		ExpiresAt: time.Now().Add(-48 * time.Hour), // expired two days ago
		Modules:   []string{license.ModulePlatformAdmin},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// A short, one-hour grace period, well short of the 48h-past-expiry
	// above, so this token is genuinely past grace, not within it.
	v, err := license.NewVerifier(true, pubStr, tokenStr, time.Hour)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	err = v.Allow(license.ModulePlatformAdmin)
	if !errors.Is(err, license.ErrDenied) {
		t.Fatalf("expected ErrDenied for an expired-past-grace token, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"expired", "grace period", "ZONARYOS_LICENSE_GRACE_PERIOD", "firm-acme"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error message to mention %q, got: %s", want, msg)
		}
	}
}

// TestAllow_ExpiredWithinGracePeriod_StillSucceeds proves the grace
// period mechanism actually grants extra time rather than being
// decorative: a token expired slightly in the past, but still within its
// grace window, is still allowed.
func TestAllow_ExpiredWithinGracePeriod_StillSucceeds(t *testing.T) {
	pub, priv := mustKeypairVerifier(t)
	pubStr := base64.RawURLEncoding.EncodeToString(pub)

	tokenStr, err := license.Sign(priv, license.Token{
		IssuedTo:  "firm-acme",
		ExpiresAt: time.Now().Add(-time.Hour), // expired one hour ago
		Modules:   []string{license.ModulePlatformAdmin},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	v, err := license.NewVerifier(true, pubStr, tokenStr, 72*time.Hour)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if err := v.Allow(license.ModulePlatformAdmin); err != nil {
		t.Fatalf("expected an expired-but-within-grace token to still be allowed, got %v", err)
	}
}

func TestAllow_WrongSignerToken_FailsClosed(t *testing.T) {
	realPub, _ := mustKeypairVerifier(t)
	_, wrongPriv := mustKeypairVerifier(t)
	pubStr := base64.RawURLEncoding.EncodeToString(realPub)

	tokenStr, err := license.Sign(wrongPriv, license.Token{
		IssuedTo:  "attacker-forged",
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		Modules:   []string{license.ModulePlatformAdmin},
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	v, err := license.NewVerifier(true, pubStr, tokenStr, 0)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if err := v.Allow(license.ModulePlatformAdmin); !errors.Is(err, license.ErrDenied) {
		t.Fatalf("expected a token signed by the wrong private key to be denied, got %v", err)
	}
}
