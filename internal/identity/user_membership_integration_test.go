// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package identity_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// seedRoleAndMembership creates an "owner" role in firmID and assigns
// userID to it, going through WithFirmContext exactly as application code
// would (not as a superuser bypassing RLS).
func seedRoleAndMembership(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, roleID *uuid.UUID) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (firm_id, key, name) VALUES ($1, 'owner', 'Owner') RETURNING id
		`, firmID).Scan(roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)
		`, firmID, userID, *roleID)
		return err
	})
}

// These tests exercise the identity <-> firm/role model wiring against a
// real Postgres instance - no Keycloak involved, since ResolveOrCreateUser,
// Memberships, and RoleInFirm only ever see an already-verified Identity
// value, never a raw token. They are skipped unless
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set (same convention as internal/platform/db's RLS tests).

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run identity integration tests")
	}
	return adminDSN, appDSN
}

func TestResolveOrCreateUser_IsIdempotentAndUpdatesProfile(t *testing.T) {
	adminDSN, appDSN := testPools(t)
	ctx := context.Background()

	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	adminPool, err := zdb.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()
	if _, err := adminPool.Exec(ctx, `TRUNCATE firms, users, roles, role_permissions, user_firm_roles CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err := zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	defer appPool.Close()

	id := identity.Identity{Subject: "kc-subject-1", Email: "old@example.com", DisplayName: "Old Name"}

	firstID, err := identity.ResolveOrCreateUser(ctx, appPool, id)
	if err != nil {
		t.Fatalf("first ResolveOrCreateUser: %v", err)
	}

	// Same Keycloak subject, updated profile fields - simulates the user
	// having changed their name/email in Keycloak between logins.
	id.Email = "new@example.com"
	id.DisplayName = "New Name"
	secondID, err := identity.ResolveOrCreateUser(ctx, appPool, id)
	if err != nil {
		t.Fatalf("second ResolveOrCreateUser: %v", err)
	}

	if firstID != secondID {
		t.Fatalf("expected the same user row for the same keycloak subject, got %s then %s", firstID, secondID)
	}

	var email, displayName string
	err = adminPool.QueryRow(ctx, `SELECT email, display_name FROM users WHERE id = $1`, firstID).Scan(&email, &displayName)
	if err != nil {
		t.Fatalf("query updated user: %v", err)
	}
	if email != "new@example.com" || displayName != "New Name" {
		t.Fatalf("expected profile to be updated to (new@example.com, New Name), got (%s, %s)", email, displayName)
	}
}

func TestMembershipsAndRoleInFirm_FullChain(t *testing.T) {
	adminDSN, appDSN := testPools(t)
	ctx := context.Background()

	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	adminPool, err := zdb.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()
	if _, err := adminPool.Exec(ctx, `TRUNCATE firms, users, roles, role_permissions, user_firm_roles CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err := zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	defer appPool.Close()

	userID, err := identity.ResolveOrCreateUser(ctx, appPool, identity.Identity{
		Subject: "kc-subject-2", Email: "carol@example.com", DisplayName: "Carol",
	})
	if err != nil {
		t.Fatalf("resolve user: %v", err)
	}

	var firmA, firmB, roleA uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B (not a member)') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}

	if err := seedRoleAndMembership(ctx, appPool, firmA, userID, &roleA); err != nil {
		t.Fatalf("seed role + membership: %v", err)
	}

	memberships, err := identity.Memberships(ctx, appPool, userID)
	if err != nil {
		t.Fatalf("Memberships: %v", err)
	}
	if len(memberships) != 1 {
		t.Fatalf("expected exactly 1 membership (firm A only, not firm B), got %d", len(memberships))
	}
	if memberships[0].FirmID != firmA {
		t.Fatalf("expected membership in firm A (%s), got %s", firmA, memberships[0].FirmID)
	}
	if memberships[0].FirmName != "Firm A" {
		t.Fatalf("expected firm name 'Firm A', got %q", memberships[0].FirmName)
	}

	detail, err := identity.RoleInFirm(ctx, appPool, firmA, userID)
	if err != nil {
		t.Fatalf("RoleInFirm: %v", err)
	}
	if detail.RoleKey != "owner" || detail.RoleName != "Owner" {
		t.Fatalf("expected role (owner, Owner), got (%s, %s)", detail.RoleKey, detail.RoleName)
	}

	// The user has no membership in firm B - RLS must reject this, not
	// just return an empty result silently swallowed by application code.
	if _, err := identity.RoleInFirm(ctx, appPool, firmB, userID); err == nil {
		t.Fatal("expected RoleInFirm to fail for a firm the user does not belong to, got nil error")
	}
}
