// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package cryptutil is a small, dependency-free leaf package holding the
// AES-256-GCM encrypt-at-rest primitive this codebase uses wherever a
// secret needs a RECOVERABLE plaintext (unlike a one-way hash, used
// wherever only an equality check is ever needed - see Encryptor's own
// doc comment for the full reasoning). Originally built for
// internal/ai's own AI provider API keys; extracted into this leaf
// package (the same "extract into a dependency-free package to avoid an
// import cycle" move internal/queryfilter's own doc comment already
// documents for internal/reports' filter-clause logic) so
// internal/integration's own connector-secret encryption (data pipeline/
// ETL/system integrations batch) can reuse the identical primitive
// without importing internal/ai - internal/ai transitively imports
// internal/reports, which imports internal/workflow, which imports
// internal/invoicing, which needs to import internal/integration for its
// own outbound push; internal/integration importing internal/ai directly
// would complete that cycle. internal/ai's own Encryptor
// (internal/ai/crypto.go) is now a thin wrapper around this package's
// Encryptor, keeping internal/ai's own existing public API and error
// semantics (ai.ErrEncryptionKeyNotSet) completely unchanged for its own
// callers.
package cryptutil

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrKeyNotSet means the Encryptor a caller is trying to Encrypt/Decrypt
// through is nil - see NewEncryptor's own doc comment for the default-off
// contract that makes this possible. Each CALLING package (internal/ai,
// internal/integration) defines and returns its own, more specifically-
// worded sentinel error instead of this one wherever it already nil-
// checks its own *Encryptor before calling Encrypt/Decrypt (which is
// every real call site in both packages) - this generic error only
// actually surfaces from Encrypt/Decrypt's own defensive nil-receiver
// guard, which application code never normally reaches.
var ErrKeyNotSet = errors.New("encryption key is not configured")

// Encryptor holds a server-side AES-256-GCM key used to encrypt/decrypt
// a secret at rest.
//
// AES-256-GCM, not hashing: every secret-at-rest in this codebase that
// only ever needs an equality check (edge_agent_tokens.token_hash,
// internal/apikey's own key hash) uses a one-way SHA-256 digest instead.
// A secret this package's own callers store is different in kind - the
// PLAINTEXT must be recoverable (to call an AI provider's own API, or to
// authenticate against an integration connector's own target), so a
// one-way hash is structurally unusable; AES-256-GCM gives both
// confidentiality AND tamper-detection (its built-in auth tag), not just
// confidentiality the way plain AES-CBC would.
//
// Key protection: the base64Key a caller passes to NewEncryptor is read
// once at server startup (internal/platform/config) and passed in
// explicitly (never read from os.Getenv inside this package itself) - it
// never touches the database, is never logged, and is not itself stored
// anywhere: losing it means every value ever encrypted under it becomes
// permanently unrecoverable (the correct failure mode for a server-side-
// only secret - there is no key-recovery mechanism, the same posture
// internal/license's own Ed25519 keypair handling takes for its own
// asymmetric key).
type Encryptor struct {
	key []byte
}

// NewEncryptor builds an Encryptor from base64Key. Returns (nil, nil) -
// not an error - when base64Key is empty, the same default-off nil-
// pointer contract internal/edgeagent.NewNATSBridge/internal/license.NewVerifier
// already establish for an optional subsystem: every caller checks for a
// nil *Encryptor first and returns its OWN sentinel error at the point
// encryption/decryption is actually attempted, rather than failing
// server startup outright.
//
// A NON-empty but malformed value (invalid base64, or a decoded length
// other than exactly 32 bytes - AES-256 requires a 256-bit/32-byte key)
// IS a real error: an operator who explicitly set the env var made a
// mistake worth failing loudly on, the same "unset is fine, set-but-
// wrong is not" posture ZONARYOS_LICENSE_GRACE_PERIOD's own parsing in
// internal/platform/config.Load already takes. keyEnvVarName is included
// in the returned error text so a caller doesn't need to wrap it itself
// just to say which env var was malformed.
func NewEncryptor(base64Key, keyEnvVarName string) (*Encryptor, error) {
	if base64Key == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid base64: %w", keyEnvVarName, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s must decode to exactly 32 bytes (AES-256), got %d", keyEnvVarName, len(key))
	}
	return &Encryptor{key: key}, nil
}

// Encrypt returns plaintext encrypted with AES-256-GCM, as a single
// base64-encoded string (a fresh random nonce, prepended to the
// ciphertext+auth-tag GCM already produces - the standard
// crypto/cipher.AEAD.Seal convention, self-contained so Decrypt needs
// nothing else stored alongside it). Every call generates a fresh
// nonce (crypto/rand, never reused - GCM's security guarantee depends
// entirely on nonce uniqueness per key).
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if e == nil {
		return "", ErrKeyNotSet
	}
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("init AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("init GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt reverses Encrypt. Returns an error (never a partially-decrypted
// value) if the ciphertext was tampered with or was encrypted under a
// different key - GCM's authentication tag makes this detectable, not
// just theoretically possible.
func (e *Encryptor) Decrypt(encoded string) (string, error) {
	if e == nil {
		return "", ErrKeyNotSet
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("init AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("init GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

// MaskAPIKey returns a display-safe form of a plaintext secret -
// "...WXYZ" (the last 4 characters only, everything else replaced by a
// fixed "...") - never the secret returned in full. Callers decrypt
// server-side, compute this, and discard the plaintext immediately -
// never sending it to a client.
func MaskAPIKey(plaintext string) string {
	const visibleSuffixLen = 4
	if len(plaintext) <= visibleSuffixLen {
		return "...."
	}
	return "..." + plaintext[len(plaintext)-visibleSuffixLen:]
}
