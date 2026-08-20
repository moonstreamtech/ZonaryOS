// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package apikey is the non-interactive/programmatic-access half of the
// webhooks/API-keys/external-integration batch: a firm owner mints a
// bearer credential (an "API key") scoped to a subset of their own
// permissions, for a script or external system to call ZonaryOS's HTTP
// API without a Keycloak user session.
//
// Authentication mirrors internal/edgeagent's own token scheme exactly
// (SHA-256 hash stored, never the plaintext; a `subtle.ConstantTimeCompare`
// on top of the SQL equality filter; the same "before any firm_id is
// known, look the hash up via an RLS carve-out function" two-step
// sequencing) - see authenticate's own doc comment for the one real
// difference: edge agent tokens fuse auth and business logic into a
// single transaction per call (internal/edgeagent.withTokenTx), since
// every edge-agent endpoint is protocol-specific; API keys instead need
// to work as generic HTTP middleware in front of EVERY existing handler
// unchanged, so authenticate is a short-lived, standalone transaction
// (validate the key, touch last_used_at) that hands off a resolved
// identity.Identity via context, the same way Keycloak bearer-token auth
// already does - not a fused business-transaction wrapper.
//
// A key authenticated request acts as its OWN CREATING USER for every
// authorization check (their firm membership, their owner flag, their
// role grants) - narrowed only by the key's own declared scopes, which
// internal/permission.Has enforces via identity.ScopeRestriction. This
// package never introduces a second identity model; it only ever
// resolves back to a real users.id, exactly as if that user had logged
// in via Keycloak, so every existing handler's own
// identity.FromContext + identity.ResolveOrCreateUser call keeps working
// completely unchanged.
package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - API key management is owner-gated.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's API keys")

	// ErrKeyNotFound means no api_keys row with the given id is visible
	// in the caller's firm context.
	ErrKeyNotFound = errors.New("API key not found")

	// ErrInvalidAPIKey means CreateAPIKey was given a structurally
	// invalid request (empty name).
	ErrInvalidAPIKey = errors.New("invalid API key request")

	// ErrScopeExceedsPermissions means a requested scope included a
	// permission_key the creating user doesn't actually hold - see
	// CreateAPIKey's own doc comment.
	ErrScopeExceedsPermissions = errors.New("requested scope exceeds the creating user's own permissions")

	// ErrInvalidToken means a presented API key doesn't match any
	// active, unexpired api_keys row.
	ErrInvalidToken = errors.New("invalid API key")
)

const auditEntityType = "api_key"

const (
	createAuditAction = "create"
	revokeAuditAction = "revoke"
	rotateAuditAction = "rotate"
)

// keyPrefix marks a value as a ZonaryOS API key at a glance (e.g. in logs
// or a pasted support message) - the same "labeled secret" convention
// internal/edgeagent's own tokenPrefix ("eat_") establishes; the random
// suffix alone carries all the entropy this scheme relies on.
const keyPrefix = "zak_"

// APIKey is one api_keys row with the secret itself deliberately absent -
// what ListAPIKeys returns, and what CreateAPIKey returns alongside the
// one-time plaintext.
type APIKey struct {
	ID         uuid.UUID
	FirmID     uuid.UUID
	Name       string
	Scopes     []string
	CreatedBy  uuid.UUID
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	IsActive   bool
	CreatedAt  time.Time
}

// generateKey returns a fresh, high-entropy plaintext API key (32 random
// bytes, base64url-encoded, keyPrefix-labeled) and its SHA-256 hex
// digest - the only form ever persisted (api_keys.key_hash).
func generateKey() (plaintext, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate API key: %w", err)
	}
	plaintext = keyPrefix + base64.RawURLEncoding.EncodeToString(buf)
	return plaintext, hashKey(plaintext), nil
}

// hashKey is the one place a presented key is turned into the form
// stored in / looked up against api_keys.key_hash.
func hashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// CreateAPIKeyInput is CreateAPIKey's request shape.
type CreateAPIKeyInput struct {
	Name      string
	Scopes    []string
	ExpiresAt *time.Time
}

func scanAPIKey(row pgx.Row) (APIKey, error) {
	var k APIKey
	if err := row.Scan(&k.ID, &k.FirmID, &k.Name, &k.Scopes, &k.CreatedBy, &k.LastUsedAt, &k.ExpiresAt, &k.IsActive, &k.CreatedAt); err != nil {
		return APIKey{}, err
	}
	return k, nil
}

// #nosec G101 -- this is a SQL column list, not a credential; gosec's
// heuristic matches on the "key" substring in "api_keys"/apiKeyColumns.
// The actual secret (key_hash) is deliberately excluded from this list -
// APIKey (the struct scanAPIKey populates from it) never carries it.
const apiKeyColumns = "id, firm_id, name, scopes, created_by, last_used_at, expires_at, is_active, created_at"

// CreateAPIKey mints a new API key for firmID, storing only its hash, and
// returns the plaintext value exactly once - the caller is responsible
// for handing it to whatever script/system will use it; ZonaryOS itself
// never has the plaintext again after this call returns. Owner-gated,
// same tier as internal/edgeagent.IssueToken.
//
// Scope validation (the design's actual safety property): every entry in
// input.Scopes must already be one of userID's OWN currently-granted
// permission_keys (permission.ListGrantedPermissionKeys) - an API key
// can never exceed the authority of the user who created it, checked
// once here, at creation time, not re-validated on every subsequent
// request the key makes (the same "resolved once" tradeoff this
// migration's own doc comment on api_keys.scopes explains: if the
// creating user's own permissions are later revoked, an already-issued
// key keeps whatever scopes it was given until it's itself revoked -
// consistent with every other "snapshot at creation" credential in this
// codebase, and correctable by revoking the key, same as a compromised
// credential anywhere else).
func CreateAPIKey(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateAPIKeyInput) (plaintext string, key APIKey, err error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return "", APIKey{}, fmt.Errorf("%w: name must not be empty", ErrInvalidAPIKey)
	}

	plaintext, hash, err := generateKey()
	if err != nil {
		return "", APIKey{}, err
	}

	txErr := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		granted, err := permission.ListGrantedPermissionKeys(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		for _, scope := range input.Scopes {
			if !slices.Contains(granted, scope) {
				return fmt.Errorf("%w: %q", ErrScopeExceedsPermissions, scope)
			}
		}
		scopes := input.Scopes
		if scopes == nil {
			scopes = []string{}
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO api_keys (firm_id, name, key_hash, scopes, created_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING `+apiKeyColumns, firmID, name, hash, scopes, userID, input.ExpiresAt)
		k, err := scanAPIKey(row)
		if err != nil {
			return fmt.Errorf("insert API key: %w", err)
		}
		key = k

		return auditlog.Write(ctx, tx, firmID, userID, key.ID, auditEntityType, createAuditAction, map[string]any{
			"name": name, "scopes": scopes,
		})
	})
	if txErr != nil {
		return "", APIKey{}, txErr
	}
	return plaintext, key, nil
}

// ListAPIKeys lists firmID's API keys, without their secrets. Owner-gated,
// same tier as CreateAPIKey.
func ListAPIKeys(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]APIKey, error) {
	var keys []APIKey
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		rows, err := tx.Query(ctx, `SELECT `+apiKeyColumns+` FROM api_keys WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list API keys: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			k, err := scanAPIKey(rows)
			if err != nil {
				return fmt.Errorf("scan API key: %w", err)
			}
			keys = append(keys, k)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// RevokeAPIKey deactivates keyID (is_active = false, never hard-deleted -
// preserves webhook-style history/audit trail even though this package
// has no delivery-history table of its own). Owner-gated, same tier as
// CreateAPIKey. A revoked key fails authenticate immediately, same
// "checked at the DB row, not cached" guarantee edge_agent_tokens gives.
func RevokeAPIKey(ctx context.Context, pool *pgxpool.Pool, firmID, userID, keyID uuid.UUID) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		tag, err := tx.Exec(ctx, `UPDATE api_keys SET is_active = false WHERE id = $1 AND firm_id = $2`, keyID, firmID)
		if err != nil {
			return fmt.Errorf("revoke API key: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrKeyNotFound
		}

		return auditlog.Write(ctx, tx, firmID, userID, keyID, auditEntityType, revokeAuditAction, nil)
	})
}

// RotateAPIKey replaces keyID's own key_hash in place with a freshly
// generated one, returning the new plaintext exactly once - same
// one-time-reveal contract CreateAPIKey's own doc comment establishes.
// The row's own id/name/scopes/created_by/expires_at are untouched (this
// is a credential-rotation operation, not a re-creation): a caller that
// has this key's ID recorded elsewhere (a scheduled job's own config,
// say) doesn't need to update anything except the plaintext value
// itself. last_used_at is reset to nil - the newly rotated secret has,
// by definition, never yet been used. The OLD plaintext is immediately
// invalidated: nothing about a UPDATE-in-place leaves the previous
// key_hash valid even for an instant (unlike, say, a dual-active-keys
// rotation scheme - deliberately not built here, see this batch's own
// scope boundary). Owner-gated, same tier as CreateAPIKey/RevokeAPIKey.
func RotateAPIKey(ctx context.Context, pool *pgxpool.Pool, firmID, userID, keyID uuid.UUID) (plaintext string, key APIKey, err error) {
	plaintext, hash, err := generateKey()
	if err != nil {
		return "", APIKey{}, err
	}

	txErr := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		row := tx.QueryRow(ctx, `
			UPDATE api_keys SET key_hash = $1, last_used_at = NULL WHERE id = $2 AND firm_id = $3
			RETURNING `+apiKeyColumns, hash, keyID, firmID)
		k, err := scanAPIKey(row)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrKeyNotFound
			}
			return fmt.Errorf("rotate API key: %w", err)
		}
		key = k

		return auditlog.Write(ctx, tx, firmID, userID, keyID, auditEntityType, rotateAuditAction, nil)
	})
	if txErr != nil {
		return "", APIKey{}, txErr
	}
	return plaintext, key, nil
}
