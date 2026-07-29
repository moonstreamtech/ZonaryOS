// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package identity

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Verifier validates bearer tokens issued by a single OIDC issuer (the
// ZonaryOS Keycloak realm) and extracts the caller's Identity from them.
type Verifier struct {
	oidcVerifier *oidc.IDTokenVerifier
	clientID     string
}

// NewVerifier discovers the issuer's OIDC configuration (including its
// JWKS endpoint) and builds a Verifier that checks tokens against it.
func NewVerifier(ctx context.Context, issuerURL, clientID string) (*Verifier, error) {
	if clientID == "" {
		return nil, fmt.Errorf("clientID must not be empty")
	}

	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("discover oidc provider %q: %w", issuerURL, err)
	}

	// Keycloak access tokens (as opposed to ID tokens) carry the client
	// that requested them in "azp", not necessarily in "aud" - by default
	// "aud" is just "account" unless a custom audience mapper is added.
	// SkipClientIDCheck disables go-oidc's built-in "aud contains
	// clientID" check; Verify below checks "azp" itself instead.
	v := provider.Verifier(&oidc.Config{SkipClientIDCheck: true})

	return &Verifier{oidcVerifier: v, clientID: clientID}, nil
}

// keycloakClaims covers the standard OIDC claims plus Keycloak's "azp"
// (authorized party: the client ID that requested the token).
type keycloakClaims struct {
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	AuthorizedParty   string `json:"azp"`
}

// Verify checks rawToken's signature, issuer, expiry, and authorized party,
// returning the Identity it asserts. It does not consult ZonaryOS's own
// database - see ResolveOrCreateUser for that.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (Identity, error) {
	idToken, err := v.oidcVerifier.Verify(ctx, rawToken)
	if err != nil {
		return Identity{}, fmt.Errorf("verify token: %w", err)
	}

	var claims keycloakClaims
	if err := idToken.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("parse token claims: %w", err)
	}

	if claims.AuthorizedParty != v.clientID {
		return Identity{}, fmt.Errorf("token authorized party %q does not match expected client %q", claims.AuthorizedParty, v.clientID)
	}
	if claims.Subject == "" {
		return Identity{}, fmt.Errorf("token missing subject claim")
	}

	displayName := claims.Name
	if displayName == "" {
		displayName = claims.PreferredUsername
	}

	return Identity{
		Subject:     claims.Subject,
		Email:       claims.Email,
		DisplayName: displayName,
	}, nil
}
