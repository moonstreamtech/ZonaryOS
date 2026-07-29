// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package identity resolves an authenticated Keycloak user into the
// ZonaryOS identity/firm model from internal/platform/db: verifying the
// bearer token proves who the caller is; everything about which firms they
// belong to and what role they hold in each comes entirely from ZonaryOS's
// own tables (users, firms, roles, user_firm_roles), never from Keycloak
// itself. Keycloak is an identity provider only - see docs/DEVELOPMENT.md.
package identity

import "context"

// Identity is what a verified token tells us about the caller: who they
// are, nothing about what they're allowed to do.
type Identity struct {
	// Subject is the stable Keycloak "sub" claim - the foreign key into
	// ZonaryOS's own users.keycloak_subject column.
	Subject string
	Email   string
	// DisplayName falls back to the token's preferred_username when the
	// issuer didn't supply a "name" claim.
	DisplayName string
}

type contextKey int

const identityContextKey contextKey = iota

// WithIdentity attaches id to ctx, for retrieval via FromContext.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityContextKey, id)
}

// FromContext retrieves the Identity attached by Middleware, if any.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityContextKey).(Identity)
	return id, ok
}
