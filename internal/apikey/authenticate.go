// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apikey

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// Fallback is identity.Verifier.Fallback's own implementation, backed by
// pool - wired once in cmd/server/main.go
// (verifier.Fallback = &apikey.Fallback{Pool: pool}).
type Fallback struct {
	Pool *pgxpool.Pool
}

// bearerToken mirrors internal/identity's own unexported copy - small
// enough that redeclaring it here is simpler than exporting it just for
// this one caller.
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

// Authenticate implements identity.Fallback. A token without keyPrefix is
// rejected immediately (ok=false, not an error) without touching the
// database at all - the cheap check that lets identity.Middleware's
// fallback path cost nothing for the overwhelmingly common case (a
// malformed/expired Keycloak JWT, not an API key). firmID comes from
// r.PathValue("firmID") - available already at this point in the chain,
// since net/http's own ServeMux resolves path parameters as part of
// routing, before invoking the matched handler chain (which Middleware
// is the outermost link of) - this is what makes the cross-firm
// rejection test case work: a key created for firm A presented against
// `/api/firms/B/...` simply won't match any row (the lookup below filters
// by firm_id = the URL's own firmID), the same way it wouldn't for any
// other caller who isn't a member of firm B.
func (f *Fallback) Authenticate(r *http.Request) (identity.Identity, []string, bool) {
	token, ok := bearerToken(r)
	if !ok || !strings.HasPrefix(token, keyPrefix) {
		return identity.Identity{}, nil, false
	}
	firmID, err := uuid.Parse(r.PathValue("firmID"))
	if err != nil {
		return identity.Identity{}, nil, false
	}

	id, scopes, err := authenticate(r.Context(), f.Pool, firmID, token)
	if err != nil {
		return identity.Identity{}, nil, false
	}
	return id, scopes, true
}

// authenticate is the actual hash-lookup + constant-time-compare +
// expiry/active check, in a short-lived transaction of its own - see this
// package's doc comment for why this is a standalone transaction (touch
// last_used_at, resolve an identity.Identity) rather than
// internal/edgeagent.withTokenTx's fused "auth plus the entire handler's
// business logic in one transaction" shape: every existing HTTP handler
// downstream of identity.Middleware already opens its OWN transaction via
// zdb.WithFirmContext once it has a resolved userID, and this needs to
// interoperate with all of them unchanged, not just a handful of
// purpose-built ones.
//
// ciaudit:ignore-firmid-check: this IS the authentication step itself,
// called before any userID is known - there is no caller identity yet to
// run permission.IsMember/Has/IsOwner against. firmID here scopes the
// api_keys lookup to the URL's own firm (the cross-firm-rejection
// property), it is not an authorization decision; every downstream
// handler this resolves an identity for still runs its own normal
// permission checks against the resolved userID.
func authenticate(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID, plaintext string) (identity.Identity, []string, error) {
	hash := hashKey(plaintext)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return identity.Identity{}, nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_api_key_hash', $1, true)", hash); err != nil {
		return identity.Identity{}, nil, err
	}

	var keyID, createdBy uuid.UUID
	var scopes []string
	var storedHash string
	var isActive bool
	var expiresAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, created_by, scopes, key_hash, is_active, expires_at
		FROM api_keys WHERE key_hash = $1 AND firm_id = $2
	`, hash, firmID).Scan(&keyID, &createdBy, &scopes, &storedHash, &isActive, &expiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return identity.Identity{}, nil, ErrInvalidToken
		}
		return identity.Identity{}, nil, err
	}
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(hash)) != 1 {
		return identity.Identity{}, nil, ErrInvalidToken
	}
	if !isActive {
		return identity.Identity{}, nil, ErrInvalidToken
	}
	if expiresAt != nil && expiresAt.Before(time.Now()) {
		return identity.Identity{}, nil, ErrInvalidToken
	}

	// Only now, with a validated key's real firm_id in hand, is the
	// ordinary tenant-scoping session setting established - api_keys'
	// own RLS policy's WITH CHECK clause (unlike its USING clause) is
	// NOT widened by the key_hash carve-out (see the migration's own doc
	// comment on api_keys_tenant_isolation), so the UPDATE below would
	// otherwise be rejected by RLS. Mirrors
	// internal/edgeagent.withTokenTx's identical two-step sequencing.
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_firm_id', $1, true)", firmID.String()); err != nil {
		return identity.Identity{}, nil, err
	}

	var subject, email, displayName string
	if err := tx.QueryRow(ctx, `SELECT keycloak_subject, email, display_name FROM users WHERE id = $1`, createdBy).
		Scan(&subject, &email, &displayName); err != nil {
		return identity.Identity{}, nil, err
	}

	if _, err := tx.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, keyID); err != nil {
		return identity.Identity{}, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return identity.Identity{}, nil, err
	}

	if scopes == nil {
		scopes = []string{}
	}
	return identity.Identity{Subject: subject, Email: email, DisplayName: displayName}, scopes, nil
}
