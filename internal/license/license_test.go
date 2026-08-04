// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package license_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/license"
)

func mustKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	return pub, priv
}

// TestSignVerify_ValidSignatureVerifies is unit requirement 1: a token
// signed with the private key half of a keypair verifies successfully
// against the matching public key, and round-trips its fields exactly.
func TestSignVerify_ValidSignatureVerifies(t *testing.T) {
	pub, priv := mustKeypair(t)
	want := license.Token{
		IssuedTo:  "firm-acme-001",
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second),
		Modules:   []string{license.ModulePlatformAdmin, license.ModuleAuditTrail},
	}

	tokenStr, err := license.Sign(priv, want)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	got, err := license.Verify(tokenStr, pub)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if got.IssuedTo != want.IssuedTo {
		t.Errorf("IssuedTo: got %q, want %q", got.IssuedTo, want.IssuedTo)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("ExpiresAt: got %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
	if len(got.Modules) != len(want.Modules) || got.Modules[0] != want.Modules[0] || got.Modules[1] != want.Modules[1] {
		t.Errorf("Modules: got %v, want %v", got.Modules, want.Modules)
	}
}

// TestVerify_TamperedTokenFails is unit requirement 2: flipping a single
// byte in a validly signed token invalidates its signature.
func TestVerify_TamperedTokenFails(t *testing.T) {
	pub, priv := mustKeypair(t)
	tok := license.Token{IssuedTo: "firm-x", ExpiresAt: time.Now().Add(time.Hour), Modules: []string{license.ModulePlatformAdmin}}

	tokenStr, err := license.Sign(priv, tok)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	tampered := []byte(tokenStr)
	// Flip a byte partway through the payload segment (before the "."),
	// deliberately not the very first byte, so this stays a valid-looking
	// base64url string rather than triggering the decode-error path -
	// this test is specifically about signature invalidation, not
	// malformed-encoding rejection.
	dot := strings.IndexByte(tokenStr, '.')
	if dot < 2 {
		t.Fatalf("unexpected token shape: %q", tokenStr)
	}
	flipIdx := dot / 2
	tampered[flipIdx] ^= 0x01

	if _, err := license.Verify(string(tampered), pub); err == nil {
		t.Fatal("expected tampered token to fail verification, got nil error")
	}
}

// TestVerify_WrongPublicKeyFails is unit requirement 4: a token signed
// with one private key fails verification against a different, unrelated
// public key (i.e. the real configured key, not the signer's).
func TestVerify_WrongPublicKeyFails(t *testing.T) {
	_, priv := mustKeypair(t)
	wrongPub, _ := mustKeypair(t)

	tok := license.Token{IssuedTo: "firm-x", ExpiresAt: time.Now().Add(time.Hour), Modules: []string{license.ModulePlatformAdmin}}
	tokenStr, err := license.Sign(priv, tok)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	_, err = license.Verify(tokenStr, wrongPub)
	if !errors.Is(err, license.ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

// TestVerify_MalformedTokenFails covers the structurally-invalid-string
// path (no ".", bad base64) distinctly from a bad signature - Verify's
// two failure modes are meant to be told apart via errors.Is.
func TestVerify_MalformedTokenFails(t *testing.T) {
	pub, _ := mustKeypair(t)

	if _, err := license.Verify("not-a-real-token", pub); !errors.Is(err, license.ErrMalformedToken) {
		t.Errorf("no separator: expected ErrMalformedToken, got %v", err)
	}
	if _, err := license.Verify("not!base64.also!not", pub); !errors.Is(err, license.ErrMalformedToken) {
		t.Errorf("bad base64: expected ErrMalformedToken, got %v", err)
	}
}

func TestParsePublicKey_RejectsWrongLength(t *testing.T) {
	if _, err := license.ParsePublicKey("dG9vc2hvcnQ"); err == nil {
		t.Fatal("expected an error for a too-short key")
	}
}
