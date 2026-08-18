// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// Encryptor holds the server-side AES-256-GCM key
// (ZONARYOS_AI_ENCRYPTION_KEY) used to encrypt/decrypt a firm's AI
// provider API key at rest.
//
// AES-256-GCM, not hashing: every other secret-at-rest in this codebase
// (edge_agent_tokens.token_hash, internal/apikey's own key hash) is a
// one-way SHA-256 digest, because the only thing ever needed again is an
// equality check against a caller-presented value. An AI provider API
// key is different in kind - internal/ai must present the PLAINTEXT key
// to the provider's own API on every suggest-workflow/suggest-report/
// anomaly-detection call, so a one-way hash is structurally
// unusable here; the plaintext must be recoverable, which is exactly
// what authenticated symmetric encryption (AES-256-GCM: confidentiality
// AND tamper-detection via its built-in auth tag, not just
// confidentiality the way plain AES-CBC would give) is for.
//
// Key protection: ZONARYOS_AI_ENCRYPTION_KEY is a 32-byte, base64-
// encoded value read once at server startup (internal/platform/config)
// and passed into this package explicitly (never read from os.Getenv
// inside this package itself, following this codebase's usual
// config-through-Config-struct convention) - it never touches the
// database, is never logged, and is not itself stored anywhere: losing
// it means every stored ai_configurations.api_key_encrypted value
// becomes permanently unrecoverable (the correct failure mode for a
// server-side-only secret - there is no key-recovery mechanism, the
// same posture internal/license's own Ed25519 keypair handling takes
// for its own asymmetric key). Operators are responsible for backing
// this value up out-of-band (a secrets manager, not this repository) -
// see docs/DEVELOPMENT.md's own note on this.
type Encryptor struct {
	key []byte
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
	if base64Key == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return nil, fmt.Errorf("ZONARYOS_AI_ENCRYPTION_KEY is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("ZONARYOS_AI_ENCRYPTION_KEY must decode to exactly 32 bytes (AES-256), got %d", len(key))
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
		return "", ErrEncryptionKeyNotSet
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
		return "", ErrEncryptionKeyNotSet
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

// MaskAPIKey returns a display-safe form of a plaintext API key - "sk-...WXYZ"
// (the last 4 characters only, everything else replaced by a fixed
// "...") - never the API key returned in full. Used exclusively by the
// GET .../ai-config response path (handlers.go); the full plaintext this
// operates on is decrypted server-side and discarded immediately after,
// never itself sent to the client.
func MaskAPIKey(plaintext string) string {
	const visibleSuffixLen = 4
	if len(plaintext) <= visibleSuffixLen {
		return "...."
	}
	return "..." + plaintext[len(plaintext)-visibleSuffixLen:]
}
