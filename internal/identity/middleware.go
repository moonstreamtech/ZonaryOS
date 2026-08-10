// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package identity

import (
	"net/http"
	"strings"
)

// Middleware verifies the request's bearer token with verifier and, on
// success, attaches the resulting Identity to the request context (see
// FromContext) before calling next. A missing or invalid token is rejected
// with 401 - it never falls through as "unauthenticated but allowed".
//
// A token that fails Keycloak verification is tried against verifier's
// own Fallback (if set) before giving up - see Verifier.Fallback's own
// doc comment for why this is the extension point non-interactive/
// programmatic auth (internal/apikey) plugs into, rather than a second
// parameter here that every RegisterRoutes call site would need to pass.
func Middleware(verifier *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}

			id, err := verifier.Verify(r.Context(), token)
			if err != nil {
				if verifier.Fallback != nil {
					if fbID, scopes, ok := verifier.Fallback.Authenticate(r); ok {
						ctx := WithIdentity(r.Context(), fbID)
						if scopes != nil {
							ctx = WithScopeRestriction(ctx, scopes)
						}
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
				}
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}
