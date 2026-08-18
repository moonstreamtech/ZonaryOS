// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai

import (
	"errors"

	"github.com/moonstreamtech/ZonaryOS/internal/cryptutil"
)

// Encryptor holds the server-side AES-256-GCM key
// (ZONARYOS_AI_ENCRYPTION_KEY) used to encrypt/decrypt a firm's AI
// provider API key at rest.
//
// A thin wrapper around internal/cryptutil.Encryptor (the AES-256-GCM
// primitive itself, extracted into that dependency-free leaf package so
// internal/integration's own connector-secret encryption - data
// pipeline/ETL/system integrations batch - can reuse it without
// importing this package, which would complete an import cycle: this
// package imports internal/reports (suggest_report.go), which imports
// internal/workflow, which imports internal/invoicing, which needs to
// import internal/integration for its own outbound push). This wrapper
// exists purely so this package's own public API and error semantics
// (ErrEncryptionKeyNotSet's own AI-specific wording, mentioning
// ZONARYOS_AI_ENCRYPTION_KEY by name) stay exactly as they were before
// the extraction - every other file in this package (config.go,
// handlers.go, anomaly.go, suggest_workflow.go) is unchanged.
//
// See internal/cryptutil.Encryptor's own doc comment for the full
// AES-256-GCM-vs-hashing design rationale and key-protection posture.
type Encryptor struct {
	inner *cryptutil.Encryptor
}

// NewEncryptor builds an Encryptor from base64Key (ZONARYOS_AI_ENCRYPTION_KEY's
// raw value). Returns (nil, nil) - not an error - when base64Key is
// empty, the same default-off nil-pointer contract
// internal/edgeagent.NewNATSBridge/internal/license.NewVerifier already
// establish for an optional subsystem: every AI-configuration write path
// checks for a nil *Encryptor first and returns ErrEncryptionKeyNotSet
// (a clear, explicit 500 - Part 5's "key-not-set" test requirement) at
// the point encryption/decryption is actually attempted, rather than
// failing server startup outright - an installation that simply hasn't
// set up AI yet should still start and serve every other feature
// normally.
//
// A NON-empty but malformed value (invalid base64, or a decoded length
// other than exactly 32 bytes - AES-256 requires a 256-bit/32-byte key)
// IS a real error: an operator who explicitly set this env var made a
// mistake worth failing loudly on, the same "unset is fine, set-but-
// wrong is not" posture ZONARYOS_LICENSE_GRACE_PERIOD's own parsing in
// internal/platform/config.Load already takes.
func NewEncryptor(base64Key string) (*Encryptor, error) {
	inner, err := cryptutil.NewEncryptor(base64Key, "ZONARYOS_AI_ENCRYPTION_KEY")
	if err != nil {
		return nil, err
	}
	if inner == nil {
		return nil, nil
	}
	return &Encryptor{inner: inner}, nil
}

// Encrypt returns plaintext encrypted with AES-256-GCM, as a single
// base64-encoded string. See internal/cryptutil.Encryptor.Encrypt's own
// doc comment for the nonce/ciphertext format.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if e == nil {
		return "", ErrEncryptionKeyNotSet
	}
	ciphertext, err := e.inner.Encrypt(plaintext)
	if err != nil {
		if errors.Is(err, cryptutil.ErrKeyNotSet) {
			return "", ErrEncryptionKeyNotSet
		}
		return "", err
	}
	return ciphertext, nil
}

// Decrypt reverses Encrypt. Returns an error (never a partially-decrypted
// value) if the ciphertext was tampered with or was encrypted under a
// different key - GCM's authentication tag makes this detectable, not
// just theoretically possible.
func (e *Encryptor) Decrypt(encoded string) (string, error) {
	if e == nil {
		return "", ErrEncryptionKeyNotSet
	}
	plaintext, err := e.inner.Decrypt(encoded)
	if err != nil {
		if errors.Is(err, cryptutil.ErrKeyNotSet) {
			return "", ErrEncryptionKeyNotSet
		}
		return "", err
	}
	return plaintext, nil
}

// MaskAPIKey returns a display-safe form of a plaintext API key - "sk-...WXYZ"
// (the last 4 characters only, everything else replaced by a fixed
// "...") - never the API key returned in full. Used exclusively by the
// GET .../ai-config response path (handlers.go); the full plaintext this
// operates on is decrypted server-side and discarded immediately after,
// never itself sent to the client.
func MaskAPIKey(plaintext string) string {
	return cryptutil.MaskAPIKey(plaintext)
}
