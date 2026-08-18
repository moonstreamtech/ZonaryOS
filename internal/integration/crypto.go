// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package integration

import (
	"fmt"

	"github.com/moonstreamtech/ZonaryOS/internal/cryptutil"
)

// sensitiveConfigKeys is the fixed set of integration_connectors.config
// keys treated as secrets - encrypted at rest, masked on read, never
// returned in full to a client. A connector config key not in this list
// (url, username, pollIntervalMinutes, ...) is stored and returned as
// plain jsonb, unencrypted - config's own free-form shape (this batch's
// own "no OAuth flow, API key/URL config only" scope boundary means every
// connector type's config is a flat, small set of string/number fields,
// not a nested credential object needing a more general scheme).
var sensitiveConfigKeys = map[string]bool{
	"apiKey":   true,
	"password": true,
	"secret":   true,
	"token":    true,
}

// encryptSensitiveConfigFields returns a copy of config with every
// sensitive key's own value replaced by its AES-256-GCM ciphertext (see
// internal/ai.Encryptor's own doc comment for why encryption, not
// hashing: the plaintext must be recoverable to actually call the
// connector's own target, unlike e.g. a webhook secret that only ever
// needs an equality/HMAC check). Reuses internal/ai's own Encryptor type
// - the AES-256-GCM primitive itself is generic (any secret needing
// encrypted-at-rest storage with a recoverable plaintext), not AI-
// specific - constructed from its OWN key, ZONARYOS_INTEGRATION_ENCRYPTION_KEY,
// deliberately a SEPARATE key from ZONARYOS_AI_ENCRYPTION_KEY: a leaked
// AI provider key must not also expose integration credentials, and vice
// versa.
//
// Returns config UNCHANGED (still nil-encryptor-safe) if config has no
// sensitive key at all - a connector whose config is just
// {"url": "..."} never needs an encryption key configured at all,
// the same "unset is fine unless actually needed" posture
// ErrEncryptionKeyNotSet's own doc comment describes.
func encryptSensitiveConfigFields(enc *cryptutil.Encryptor, config map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(config))
	for k, v := range config {
		if !sensitiveConfigKeys[k] {
			out[k] = v
			continue
		}
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%w: sensitive config field %q must be a string", ErrInvalidConnector, k)
		}
		if s == "" {
			out[k] = s
			continue
		}
		if enc == nil {
			return nil, ErrEncryptionKeyNotSet
		}
		ciphertext, err := enc.Encrypt(s)
		if err != nil {
			return nil, fmt.Errorf("encrypt config field %q: %w", k, err)
		}
		out[k] = ciphertext
	}
	return out, nil
}

// decryptSensitiveConfigFields reverses encryptSensitiveConfigFields -
// used internally (outbound.go/inbound.go/TestConnection) at the moment
// a connector's real config is actually needed to make a network call;
// never used to build an HTTP API response (see maskSensitiveConfigFields
// below for that path instead).
func decryptSensitiveConfigFields(enc *cryptutil.Encryptor, config map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(config))
	for k, v := range config {
		if !sensitiveConfigKeys[k] {
			out[k] = v
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			out[k] = v
			continue
		}
		if enc == nil {
			return nil, ErrEncryptionKeyNotSet
		}
		plaintext, err := enc.Decrypt(s)
		if err != nil {
			return nil, fmt.Errorf("decrypt config field %q: %w", k, err)
		}
		out[k] = plaintext
	}
	return out, nil
}

// preserveUnsetSensitiveFields overlays, onto encryptedConfig (the output
// of encryptSensitiveConfigFields applied to a caller's NEW input), the
// EXISTING already-encrypted ciphertext for every sensitive key the caller
// left blank or omitted entirely - UpdateConnector's own "leave a
// sensitive field blank in the edit form to keep it unchanged" contract.
// Never re-encrypts the preserved value (it's already ciphertext; doing
// so would double-encrypt it into an undecryptable value) and never
// touches a key the caller DID provide a non-empty new value for.
func preserveUnsetSensitiveFields(encryptedConfig, existingConfig map[string]any) map[string]any {
	for k := range sensitiveConfigKeys {
		if s, ok := encryptedConfig[k].(string); ok && s != "" {
			continue
		}
		existing, ok := existingConfig[k].(string)
		if !ok || existing == "" {
			continue
		}
		encryptedConfig[k] = existing
	}
	return encryptedConfig
}

// maskSensitiveConfigFields returns a copy of config safe to include in
// an HTTP response - every sensitive key's own value replaced by
// cryptutil.MaskAPIKey's own "...WXYZ" (last 4 characters only) display form,
// computed from the DECRYPTED plaintext (discarded immediately after)
// so the masked form actually reflects the real secret's own tail, the
// same "decrypt server-side only to compute a masked display string,
// never return the plaintext itself" contract internal/ai's own
// handlers.go establishes for AI provider keys.
func maskSensitiveConfigFields(enc *cryptutil.Encryptor, config map[string]any) (map[string]any, error) {
	decrypted, err := decryptSensitiveConfigFields(enc, config)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(config))
	for k, v := range decrypted {
		if !sensitiveConfigKeys[k] {
			out[k] = v
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			out[k] = ""
			continue
		}
		out[k] = cryptutil.MaskAPIKey(s)
	}
	return out, nil
}
