// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package permission checks a user's permission grants within a firm,
// against the global permission catalog and per-firm role grants
// established in migrations/0001_core_schema.up.sql (`permissions`,
// `roles`, `role_permissions`, `user_firm_roles`). This is the Never-
// Violate Rule 7 "permission tag" check - any module gating an action
// behind a permission key (the workflow engine is the first) should use
// this, not a bespoke check of its own.
package permission

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// IsMember reports whether userID belongs to firmID at all, through any
// role. This is the check every read path that isn't already gated by a
// specific Has call must still perform: WithFirmContext's RLS scoping
// only confines a query to rows whose firm_id matches the firmID the
// caller passed in - it says nothing about whether the caller is
// actually a member of that firm. Without this check, any authenticated
// user could read any firm's data just by supplying that firm's real ID,
// since the ID itself is what makes the RLS-scoped rows visible. Must be
// called within a transaction already scoped to firmID via
// db.WithFirmContext, same as Has.
//
// ciaudit:ignore-firmid-check: this *is* one of the permission-check
// primitives cmd/ciaudit looks for; it cannot call itself.
func IsMember(ctx context.Context, tx pgx.Tx, firmID, userID uuid.UUID) (bool, error) {
	var member bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_firm_roles WHERE user_id = $1 AND firm_id = $2
		)
	`, userID, firmID).Scan(&member)
	if err != nil {
		return false, fmt.Errorf("check membership: %w", err)
	}
	return member, nil
}

// CheckMembership is IsMember's pool-based counterpart, for callers that
// don't already have an open, firm-scoped transaction - e.g. an SSE
// handler that needs to check membership once before holding a
// long-lived connection open (see internal/permission.handleEvents).
func CheckMembership(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) (bool, error) {
	var member bool
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		m, err := IsMember(ctx, tx, firmID, userID)
		member = m
		return err
	})
	if err != nil {
		return false, err
	}
	return member, nil
}

// Has reports whether userID holds permissionKey in firmID, through any of
// the roles they've been assigned there (a user can hold more than one
// role per firm - see user_firm_roles's unique constraint). Must be called
// within a transaction already scoped to firmID via db.WithFirmContext;
// firmID is passed explicitly here only so the query is unambiguous even
// though RLS also confines it.
//
// If ctx carries a scope restriction (identity.ScopeRestriction - set by
// identity.Middleware only for an API-key-authenticated request, see
// internal/apikey's own doc comment), permissionKey must ALSO appear in
// that allow-list, on top of the ordinary role-based grant checked below
// - an API key narrows what its own bearer can do, it never widens it.
// An ordinary Keycloak-authenticated request carries no scope
// restriction at all, so this check is a no-op for every request that
// isn't API-key-authenticated - Has behaves exactly as it did before
// scopes existed.
//
// ciaudit:ignore-firmid-check: this *is* one of the permission-check
// primitives cmd/ciaudit looks for; it cannot call itself.
func Has(ctx context.Context, tx pgx.Tx, firmID, userID uuid.UUID, permissionKey string) (bool, error) {
	if scopes, ok := identity.ScopeRestriction(ctx); ok && !slices.Contains(scopes, permissionKey) {
		return false, nil
	}

	var has bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM user_firm_roles ufr
			JOIN role_permissions rp ON rp.role_id = ufr.role_id
			WHERE ufr.user_id = $1 AND ufr.firm_id = $2 AND rp.permission_key = $3
		)
	`, userID, firmID, permissionKey).Scan(&has)
	if err != nil {
		return false, fmt.Errorf("check permission %q: %w", permissionKey, err)
	}
	return has, nil
}

// ListGrantedPermissionKeys returns every distinct permission_key userID
// holds in firmID, through any role - the same query
// GetFirmPermissionAudit's own MyPermissionKeys computes inline, factored
// out here so internal/apikey.CreateAPIKey can validate that a new key's
// requested scopes never exceed the creating user's own actual
// permissions (see that function's own doc comment) without duplicating
// this query a second time.
//
// ciaudit:ignore-firmid-check: internal helper - every caller
// (GetFirmPermissionAudit, internal/apikey.CreateAPIKey) has already run
// IsMember/IsOwner before reaching this call; firmID here scopes the
// query (defense in depth alongside RLS), it is not itself an
// authorization decision.
func ListGrantedPermissionKeys(ctx context.Context, tx pgx.Tx, firmID, userID uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT rp.permission_key
		FROM user_firm_roles ufr
		JOIN role_permissions rp ON rp.role_id = ufr.role_id
		WHERE ufr.user_id = $1 AND ufr.firm_id = $2
		ORDER BY rp.permission_key
	`, userID, firmID)
	if err != nil {
		return nil, fmt.Errorf("list granted permission keys: %w", err)
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}
