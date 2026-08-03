// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package license is the technical-friction layer of Open Points item 32
// ("License Verification Mechanism"), implementing the concrete answer to
// that item's open question 4 ("the actual secret is usually kept
// server-side, with the client only implementing a protocol - will this
// approach be adopted?"): YES, via asymmetric (Ed25519) signatures. This
// codebase - the "client" in that relationship, since item 34's real
// central license-issuing server does not exist yet - holds only an
// Ed25519 *public* key (ZONARYOS_LICENSE_PUBLIC_KEY, see
// internal/platform/config). It can verify a signed license token; it can
// never mint one, because it never holds the matching private key. That
// private key lives only wherever an operator running cmd/licensegen (a
// stand-in test/dev issuing tool - see that command's own doc comment)
// keeps it, never in this repository.
//
// This explicitly SUPERSEDES the Developer's original raw mechanism idea
// in docs/OPEN_POINTS.md item 32 line 60 ("a complex hexadecimal process
// ... combined with each file's MD5 ..."). MD5 is a broken hash for any
// integrity/authenticity purpose (fast collision/preimage attacks are
// public and well known) and, more fundamentally, hashing alone can't
// prove *authorized issuance* the way a signature can: anyone can compute
// an MD5 digest of anything, including a self-issued fake token. Nothing
// in this package uses MD5 or any symmetric "shared secret" scheme -
// Ed25519 signature verification is the entire mechanism.
//
// # Honest scope (Vision §5's "3-Layer Defense")
//
// This is layer 2 (technical friction) only. Layer 1 (the license
// agreement, item 20) is the real deterrent. This package is explicitly
// NOT designed to be unbypassable - a determined operator running their
// own fork can patch out the enforcement check entirely, same as any
// client-side check in an open-source codebase. The goal matches Vision
// §5's own stated realism: not "no one can run it," but "running it
// without a valid license is legally risky, technically cumbersome, and
// practically worthless" (no real module catalog, no support, no updates
// via the mechanism a real license would unlock). Don't read anything in
// this package as a claim of stronger protection than that.
//
// # Token wire format
//
// A license token is a single string:
//
//	<base64url(payload JSON)>.<base64url(Ed25519 signature over the exact payload JSON bytes)>
//
// - Both segments use unpadded base64url (encoding/base64's
// RawURLEncoding) - the same encoding internal/invite's own unguessable
// tokens use, and URL/copy-paste-safe with no padding characters to strip.
// - The signature covers the payload segment's raw JSON bytes exactly as
// produced by Sign (json.Marshal on tokenPayload) - Verify re-derives
// those same bytes by base64url-decoding the first segment and checks the
// signature against them directly; it does not re-marshal or
// canonicalize anything, so any single flipped bit in either segment
// invalidates the signature.
// - Payload JSON shape (tokenPayload below):
//
//	{
//	  "issuedTo":  "<opaque firm/installation identifier, operator-assigned>",
//	  "expiresAt": "<RFC3339 timestamp>",
//	  "modules":   ["<module key>", ...]
//	}
//
// This is deliberately the minimum the task brief asked for - firm/
// installation identifier, expiry, active module list - and deliberately
// NOT a JWT: a real JWT library/format would add header negotiation,
// algorithm confusion surface, and other generality this single, fixed
// use case (one signer, one fixed algorithm, one fixed claim shape) does
// not need. Matches this codebase's existing preference for the smallest
// mechanism that solves the concrete problem (see internal/invite's own
// token, a plain crypto/rand + base64url string, not a JWT either).
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Token is the parsed, verified contents of a license token - what
// Verify returns and what cmd/licensegen's Sign call takes as input.
type Token struct {
	// IssuedTo is an opaque firm/installation identifier this token was
	// issued to - free text, operator-assigned when the token is issued
	// (cmd/licensegen's -issued-to flag). There is no real identity
	// registry behind it yet (item 34's central-server issuance flow is
	// still undesigned) - it exists so a token is at least self-
	// describing ("who is this for") when an operator is looking at one,
	// not because this package validates it against anything.
	IssuedTo string
	// ExpiresAt is when this token stops being valid on its own -
	// Verifier.Allow additionally honors GracePeriod past this instant
	// before failing closed (see verifier.go).
	ExpiresAt time.Time
	// Modules is the list of module keys this token activates - see this
	// package's Module* constants for the small, honest, provisional set
	// this codebase actually gates today (Open Points item 30's real
	// module/pricing catalog remains fully open).
	Modules []string
}

// tokenPayload is Token's exact wire JSON shape - a separate type (rather
// than JSON tags on Token itself) so ExpiresAt's wire representation
// (RFC3339 string) is explicit and reviewable independent of Token's Go
// field type, and so Verify's signature-checked bytes are visibly "just
// this struct's json.Marshal output," not entangled with any other
// concern.
type tokenPayload struct {
	IssuedTo  string   `json:"issuedTo"`
	ExpiresAt string   `json:"expiresAt"`
	Modules   []string `json:"modules"`
}

func (t Token) toPayload() tokenPayload {
	return tokenPayload{
		IssuedTo:  t.IssuedTo,
		ExpiresAt: t.ExpiresAt.UTC().Format(time.RFC3339),
		Modules:   t.Modules,
	}
}

func (p tokenPayload) toToken() (Token, error) {
	expiresAt, err := time.Parse(time.RFC3339, p.ExpiresAt)
	if err != nil {
		return Token{}, fmt.Errorf("%w: expiresAt %q is not RFC3339: %v", ErrMalformedToken, p.ExpiresAt, err)
	}
	return Token{IssuedTo: p.IssuedTo, ExpiresAt: expiresAt, Modules: p.Modules}, nil
}

// Sentinel errors. ErrMalformedToken/ErrInvalidSignature are Verify's own
// failure modes (a structurally bad token string); Verifier.Allow (see
// verifier.go) wraps both of these, plus its own expiry/module-activation
// failures, under the single ErrDenied sentinel so callers that only need
// to decide an HTTP status code (internal/platformadmin's handler, for
// instance) can check one thing (errors.Is(err, license.ErrDenied)) while
// the wrapped error's message still carries the specific, actionable
// reason.
var (
	ErrMalformedToken   = errors.New("license: malformed token")
	ErrInvalidSignature = errors.New("license: invalid signature")
	ErrDenied           = errors.New("license: access denied")
)

// Sign builds and signs a token string for t using priv - the reference
// issuing operation, used only by cmd/licensegen (this repository never
// holds a private key). Not used anywhere in the running server.
func Sign(priv ed25519.PrivateKey, t Token) (string, error) {
	payloadJSON, err := json.Marshal(t.toPayload())
	if err != nil {
		return "", fmt.Errorf("marshal token payload: %w", err)
	}
	sig := ed25519.Sign(priv, payloadJSON)
	return base64.RawURLEncoding.EncodeToString(payloadJSON) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify decodes tokenString, checks its Ed25519 signature against pub,
// and returns the parsed Token on success. It deliberately does NOT check
// expiry - what counts as "too expired to use" also depends on the
// caller's configured grace period, which this function has no knowledge
// of; see Verifier.Check/Allow for the stateful, expiry-and-grace-aware
// layer built on top of this pure function.
func Verify(tokenString string, pub ed25519.PublicKey) (Token, error) {
	parts := strings.SplitN(tokenString, ".", 2)
	if len(parts) != 2 {
		return Token{}, fmt.Errorf("%w: expected exactly one \".\" separator", ErrMalformedToken)
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Token{}, fmt.Errorf("%w: payload segment: %v", ErrMalformedToken, err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Token{}, fmt.Errorf("%w: signature segment: %v", ErrMalformedToken, err)
	}
	if !ed25519.Verify(pub, payloadJSON, sig) {
		return Token{}, ErrInvalidSignature
	}
	var p tokenPayload
	if err := json.Unmarshal(payloadJSON, &p); err != nil {
		return Token{}, fmt.Errorf("%w: payload is not valid JSON: %v", ErrMalformedToken, err)
	}
	return p.toToken()
}

// ParsePublicKey decodes s (expected to be a base64url- or standard-
// base64-encoded 32-byte Ed25519 public key, as printed by
// `cmd/licensegen -genkey`) into an ed25519.PublicKey. Accepts either
// base64 flavor since operators may paste a key copied from a shell that
// applied different encoding conventions; both are unambiguous for a
// fixed 32-byte input.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	raw, err := decodeBase64Flexible(s)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key must be %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// ParsePrivateKey is ParsePublicKey's counterpart for the seed/private
// key half - used only by cmd/licensegen, never by the running server
// (which holds no private key at all).
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	raw, err := decodeBase64Flexible(s)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("private key must be %d bytes, got %d", ed25519.PrivateKeySize, len(raw))
	}
	return ed25519.PrivateKey(raw), nil
}

func decodeBase64Flexible(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if raw, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	return nil, errors.New("not valid base64url or base64")
}
