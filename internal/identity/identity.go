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

const (
	identityContextKey contextKey = iota
	scopeRestrictionContextKey
)

// WithIdentity attaches id to ctx, for retrieval via FromContext.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityContextKey, id)
}

// FromContext retrieves the Identity attached by Middleware, if any.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityContextKey).(Identity)
	return id, ok
}

// WithScopeRestriction attaches scopes to ctx - a non-nil allow-list of
// permission_keys that internal/permission.Has additionally requires
// (on top of the caller's ordinary role-based grant) before returning
// true, for the remainder of this request. Set only by Middleware, only
// when a Fallback resolved the request to a scope-limited identity (an
// API key - see internal/apikey.Fallback); an ordinary Keycloak login
// never carries a scope restriction, so Has behaves exactly as before
// for every request that isn't API-key-authenticated.
func WithScopeRestriction(ctx context.Context, scopes []string) context.Context {
	return context.WithValue(ctx, scopeRestrictionContextKey, scopes)
}

// ScopeRestriction retrieves the scope allow-list attached by
// WithScopeRestriction, if any. internal/permission.Has is the sole
// intended caller - see WithScopeRestriction's own doc comment.
func ScopeRestriction(ctx context.Context) ([]string, bool) {
	scopes, ok := ctx.Value(scopeRestrictionContextKey).([]string)
	return scopes, ok
}
