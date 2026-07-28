package identity_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// fakeOIDCProvider is a minimal in-process stand-in for Keycloak's OIDC
// discovery + JWKS endpoints, so the token-verification code path
// (signature, issuer, expiry, "azp") can be exercised by a real unit test
// without a live Keycloak instance. The live-Keycloak integration test
// (keycloak_integration_test.go) covers the parts this cannot: that a real
// Keycloak realm actually issues tokens shaped the way this test assumes.
type fakeOIDCProvider struct {
	server     *httptest.Server
	privateKey *rsa.PrivateKey
	kid        string
}

func newFakeOIDCProvider(t *testing.T) *fakeOIDCProvider {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	p := &fakeOIDCProvider{privateKey: key, kid: "test-key-1"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.handleDiscovery)
	mux.HandleFunc("/jwks", p.handleJWKS)
	p.server = httptest.NewServer(mux)

	t.Cleanup(p.server.Close)
	return p
}

func (p *fakeOIDCProvider) issuer() string { return p.server.URL }

func (p *fakeOIDCProvider) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                 p.issuer(),
		"jwks_uri":               p.issuer() + "/jwks",
		"authorization_endpoint": p.issuer() + "/auth",
		"token_endpoint":         p.issuer() + "/token",
	})
}

func (p *fakeOIDCProvider) handleJWKS(w http.ResponseWriter, r *http.Request) {
	pub := p.privateKey.PublicKey
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": p.kid,
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(bigEndianExponent(pub.E)),
			},
		},
	})
}

func bigEndianExponent(e int) []byte {
	b := make([]byte, 0, 4)
	for e > 0 {
		b = append([]byte{byte(e & 0xff)}, b...)
		e >>= 8
	}
	if len(b) == 0 {
		b = []byte{0}
	}
	return b
}

type tokenOpts struct {
	subject string
	email   string
	name    string
	azp     string
	issuer  string
	expiry  time.Time
}

func (p *fakeOIDCProvider) signToken(t *testing.T, opts tokenOpts) string {
	t.Helper()

	claims := jwt.MapClaims{
		"iss":   opts.issuer,
		"sub":   opts.subject,
		"email": opts.email,
		"name":  opts.name,
		"azp":   opts.azp,
		"iat":   time.Now().Unix(),
		"exp":   opts.expiry.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = p.kid

	signed, err := token.SignedString(p.privateKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func TestVerifier_AcceptsValidToken(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	ctx := context.Background()

	verifier, err := identity.NewVerifier(ctx, provider.issuer(), "zonaryos-web")
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	raw := provider.signToken(t, tokenOpts{
		subject: "keycloak-subject-123",
		email:   "alice@example.com",
		name:    "Alice Example",
		azp:     "zonaryos-web",
		issuer:  provider.issuer(),
		expiry:  time.Now().Add(time.Hour),
	})

	id, err := verifier.Verify(ctx, raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if id.Subject != "keycloak-subject-123" {
		t.Errorf("expected subject %q, got %q", "keycloak-subject-123", id.Subject)
	}
	if id.Email != "alice@example.com" {
		t.Errorf("expected email %q, got %q", "alice@example.com", id.Email)
	}
	if id.DisplayName != "Alice Example" {
		t.Errorf("expected display name %q, got %q", "Alice Example", id.DisplayName)
	}
}

func TestVerifier_FallsBackToPreferredUsername(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	ctx := context.Background()

	verifier, err := identity.NewVerifier(ctx, provider.issuer(), "zonaryos-web")
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims := jwt.MapClaims{
		"iss":                provider.issuer(),
		"sub":                "keycloak-subject-456",
		"email":              "bob@example.com",
		"preferred_username": "bob",
		"azp":                "zonaryos-web",
		"iat":                time.Now().Unix(),
		"exp":                time.Now().Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = provider.kid
	raw, err := token.SignedString(provider.privateKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	id, err := verifier.Verify(ctx, raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.DisplayName != "bob" {
		t.Errorf("expected display name to fall back to preferred_username 'bob', got %q", id.DisplayName)
	}
}

func TestVerifier_RejectsWrongAuthorizedParty(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	ctx := context.Background()

	verifier, err := identity.NewVerifier(ctx, provider.issuer(), "zonaryos-web")
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	raw := provider.signToken(t, tokenOpts{
		subject: "keycloak-subject-789",
		azp:     "some-other-client",
		issuer:  provider.issuer(),
		expiry:  time.Now().Add(time.Hour),
	})

	if _, err := verifier.Verify(ctx, raw); err == nil {
		t.Fatal("expected error for token issued to a different client, got nil")
	}
}

func TestVerifier_RejectsExpiredToken(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	ctx := context.Background()

	verifier, err := identity.NewVerifier(ctx, provider.issuer(), "zonaryos-web")
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	raw := provider.signToken(t, tokenOpts{
		subject: "keycloak-subject-000",
		azp:     "zonaryos-web",
		issuer:  provider.issuer(),
		expiry:  time.Now().Add(-time.Hour),
	})

	if _, err := verifier.Verify(ctx, raw); err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestVerifier_RejectsTokenSignedByUnknownKey(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	impostor := newFakeOIDCProvider(t) // both fixtures always use kid "test-key-1", but with different keypairs
	ctx := context.Background()

	verifier, err := identity.NewVerifier(ctx, provider.issuer(), "zonaryos-web")
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// Sign a token claiming to be from `provider`'s issuer, but with the
	// impostor's private key - the signature must not validate against
	// provider's published JWKS.
	raw := impostor.signToken(t, tokenOpts{
		subject: "keycloak-subject-111",
		azp:     "zonaryos-web",
		issuer:  provider.issuer(),
		expiry:  time.Now().Add(time.Hour),
	})

	if _, err := verifier.Verify(ctx, raw); err == nil {
		t.Fatal("expected error for token signed by an unrecognized key, got nil")
	}
}

func TestVerifier_RejectsMissingSubject(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	ctx := context.Background()

	verifier, err := identity.NewVerifier(ctx, provider.issuer(), "zonaryos-web")
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	raw := provider.signToken(t, tokenOpts{
		subject: "",
		azp:     "zonaryos-web",
		issuer:  provider.issuer(),
		expiry:  time.Now().Add(time.Hour),
	})

	if _, err := verifier.Verify(ctx, raw); err == nil {
		t.Fatal("expected error for token with no subject claim, got nil")
	}
}
