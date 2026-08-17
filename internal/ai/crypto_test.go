// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	enc, err := NewEncryptor(testKey(t))
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	if enc == nil {
		t.Fatal("expected non-nil Encryptor for a valid key")
	}

	plaintext := "sk-ant-super-secret-1234567890"
	ciphertext, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ciphertext == plaintext {
		t.Fatal("ciphertext must not equal plaintext")
	}

	got, err := enc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("expected decrypted plaintext %q, got %q", plaintext, got)
	}
}

func TestEncrypt_DifferentNoncePerCall(t *testing.T) {
	enc, err := NewEncryptor(testKey(t))
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	a, err := enc.Encrypt("same plaintext")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	b, err := enc.Encrypt("same plaintext")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if a == b {
		t.Fatal("expected two encryptions of the same plaintext to differ (fresh nonce per call)")
	}
}

func TestDecrypt_TamperedCiphertextFails(t *testing.T) {
	enc, err := NewEncryptor(testKey(t))
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	ciphertext, err := enc.Encrypt("plaintext")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	raw[len(raw)-1] ^= 0xFF
	tampered := base64.StdEncoding.EncodeToString(raw)

	if _, err := enc.Decrypt(tampered); err == nil {
		t.Fatal("expected Decrypt to fail on tampered ciphertext (GCM auth tag)")
	}
}

func TestNewEncryptor_EmptyKeyIsDefaultOff(t *testing.T) {
	enc, err := NewEncryptor("")
	if err != nil {
		t.Fatalf("expected no error for empty key, got %v", err)
	}
	if enc != nil {
		t.Fatal("expected nil Encryptor for empty key (default-off contract)")
	}
}

func TestNewEncryptor_MalformedKeyIsRealError(t *testing.T) {
	if _, err := NewEncryptor("not valid base64!!"); err == nil {
		t.Fatal("expected an error for invalid base64")
	}
	shortKey := base64.StdEncoding.EncodeToString([]byte("too short"))
	if _, err := NewEncryptor(shortKey); err == nil {
		t.Fatal("expected an error for a key that doesn't decode to 32 bytes")
	}
}

func TestEncryptor_NilReceiver_ReturnsKeyNotSet(t *testing.T) {
	var enc *Encryptor
	if _, err := enc.Encrypt("x"); err != ErrEncryptionKeyNotSet {
		t.Fatalf("expected ErrEncryptionKeyNotSet from Encrypt on nil *Encryptor, got %v", err)
	}
	if _, err := enc.Decrypt("x"); err != ErrEncryptionKeyNotSet {
		t.Fatalf("expected ErrEncryptionKeyNotSet from Decrypt on nil *Encryptor, got %v", err)
	}
}

func TestMaskAPIKey(t *testing.T) {
	if got := MaskAPIKey("sk-ant-1234567890WXYZ"); got != "...WXYZ" {
		t.Fatalf("expected \"...WXYZ\", got %q", got)
	}
	if got := MaskAPIKey("abc"); got != "...." {
		t.Fatalf("expected \"....\" for a key shorter than the visible suffix, got %q", got)
	}
}
