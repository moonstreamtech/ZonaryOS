// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package permission_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
)

// These tests cover Permission Audit Mode's business logic
// (GetFirmPermissionAudit/GrantPermission/RevokePermission): the
// is_owner exclusion (migrations/0004_role_owner_flag.up.sql), the
// owner-only gate on management actions (docs/DEVELOPMENT.md's working
// assumption), and the same firmID-membership discipline as every other
// firm-scoped function in this codebase (docs/DEVELOPMENT.md's
// Authorization checklist).

const testPermissionKey = "test.audit.action"

// seedPermissionKey upserts a permission catalog entry - permissions is a
// global, not-truncated-between-tests table (see setupTest in
// permission_integration_test.go), so this is idempotent by design, the
// same convention internal/workflow.DefineWorkflow's upsertPermission
// uses.
func seedPermissionKey(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, key string) {
	t.Helper()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO permissions (key, description) VALUES ($1, 'test permission')
		ON CONFLICT (key) DO NOTHING
	`, key); err != nil {
		t.Fatalf("seed permission key %q: %v", key, err)
	}
}

// seedRole creates a role directly (not via the wizard - internal/
// permission must not depend on internal/wizard, which itself depends on
// internal/workflow, which depends on internal/permission; a reverse
// dependency here would cycle) and, if userSubject is non-empty, a user
// holding it.
func seedRole(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID, key, name string, isOwner bool) uuid.UUID {
	t.Helper()
	var roleID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO roles (firm_id, key, name, is_owner) VALUES ($1, $2, $3, $4) RETURNING id
	`, firmID, key, name, isOwner).Scan(&roleID); err != nil {
		t.Fatalf("seed role %q: %v", key, err)
	}
	return roleID
}

func seedUserWithRole(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID, roleID uuid.UUID, keycloakSubject string) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test User").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)
	`, firmID, userID, roleID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	return userID
}

func seedGrant(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID, roleID uuid.UUID, key string) {
	t.Helper()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO role_permissions (firm_id, role_id, permission_key) VALUES ($1, $2, $3)
	`, firmID, roleID, key); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
}

func TestGetFirmPermissionAudit_ExcludesOwnerRoleAndReportsMyPermissions(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	seedPermissionKey(ctx, t, adminPool, testPermissionKey)

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	seedGrant(ctx, t, adminPool, firmID, ownerRoleID, testPermissionKey)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner")

	salesRoleID := seedRole(ctx, t, adminPool, firmID, "sales", "Sales", false)
	seedGrant(ctx, t, adminPool, firmID, salesRoleID, testPermissionKey)

	audit, err := permission.GetFirmPermissionAudit(ctx, appPool, firmID, ownerUserID)
	if err != nil {
		t.Fatalf("GetFirmPermissionAudit: %v", err)
	}

	if len(audit.MyPermissionKeys) != 1 || audit.MyPermissionKeys[0] != testPermissionKey {
		t.Errorf("expected the owner's own permission keys to include %q, got %v", testPermissionKey, audit.MyPermissionKeys)
	}

	for _, role := range audit.Roles {
		if role.RoleID == ownerRoleID {
			t.Fatalf("expected the owner-flagged role to be excluded from Roles, found it: %+v", role)
		}
	}

	found := false
	for _, role := range audit.Roles {
		if role.RoleID == salesRoleID {
			found = true
			if len(role.PermissionKeys) != 1 || role.PermissionKeys[0] != testPermissionKey {
				t.Errorf("expected sales role's permission keys to be [%q], got %v", testPermissionKey, role.PermissionKeys)
			}
		}
	}
	if !found {
		t.Fatal("expected the non-owner sales role to be listed")
	}
}

func TestGetFirmPermissionAudit_RequiresOwner(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	salesRoleID := seedRole(ctx, t, adminPool, firmID, "sales", "Sales", false)
	salesUserID := seedUserWithRole(ctx, t, adminPool, firmID, salesRoleID, "sub-sales")

	if _, err := permission.GetFirmPermissionAudit(ctx, appPool, firmID, salesUserID); !errors.Is(err, permission.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner, got: %v", err)
	}
}

func TestGetFirmPermissionAudit_NonMemberGetsFirmNotFound(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	var firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	roleB := seedRole(ctx, t, adminPool, firmB, "owner", "Owner", true)
	userB := seedUserWithRole(ctx, t, adminPool, firmB, roleB, "sub-outsider")

	// userB is a real owner of firm B, but not a member of firm A at all -
	// supplying firm A's genuine ID must not distinguish "real firm I'm
	// not in" from "unknown firm" (same discipline as Open Points item 37).
	if _, err := permission.GetFirmPermissionAudit(ctx, appPool, firmA, userB); !errors.Is(err, permission.ErrFirmNotFound) {
		t.Fatalf("expected ErrFirmNotFound, got: %v", err)
	}
}

func TestGrantPermission_SucceedsAndAudits(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	seedPermissionKey(ctx, t, adminPool, testPermissionKey)

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner")
	salesRoleID := seedRole(ctx, t, adminPool, firmID, "sales", "Sales", false)

	if err := permission.GrantPermission(ctx, appPool, firmID, ownerUserID, salesRoleID, testPermissionKey); err != nil {
		t.Fatalf("GrantPermission: %v", err)
	}

	var granted bool
	if err := adminPool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM role_permissions WHERE role_id = $1 AND permission_key = $2)
	`, salesRoleID, testPermissionKey).Scan(&granted); err != nil {
		t.Fatalf("check role_permissions: %v", err)
	}
	if !granted {
		t.Error("expected the sales role to be granted the permission")
	}

	var auditCount int
	var auditAction string
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*), max(action) FROM audit_log WHERE entity_type = 'role_permission' AND entity_id = $1
	`, salesRoleID).Scan(&auditCount, &auditAction); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if auditCount != 1 || auditAction != "grant" {
		t.Errorf("expected 1 audit_log row with action 'grant', got count=%d action=%q", auditCount, auditAction)
	}
}

func TestGrantPermission_RejectsOwnerRoleTarget(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	seedPermissionKey(ctx, t, adminPool, testPermissionKey)

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner")

	// Never trust the client to have excluded the owner role from its own
	// UI - the server must reject this independently.
	if err := permission.GrantPermission(ctx, appPool, firmID, ownerUserID, ownerRoleID, testPermissionKey); !errors.Is(err, permission.ErrOwnerRoleImmutable) {
		t.Fatalf("expected ErrOwnerRoleImmutable, got: %v", err)
	}
}

func TestGrantPermission_RequiresOwnerActor(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	seedPermissionKey(ctx, t, adminPool, testPermissionKey)

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	salesRoleID := seedRole(ctx, t, adminPool, firmID, "sales", "Sales", false)
	salesUserID := seedUserWithRole(ctx, t, adminPool, firmID, salesRoleID, "sub-sales")
	otherRoleID := seedRole(ctx, t, adminPool, firmID, "support", "Support", false)

	if err := permission.GrantPermission(ctx, appPool, firmID, salesUserID, otherRoleID, testPermissionKey); !errors.Is(err, permission.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner, got: %v", err)
	}

	var granted bool
	if err := adminPool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM role_permissions WHERE role_id = $1 AND permission_key = $2)
	`, otherRoleID, testPermissionKey).Scan(&granted); err != nil {
		t.Fatalf("check role_permissions: %v", err)
	}
	if granted {
		t.Error("expected no grant to have happened for a non-owner actor")
	}
}

func TestGrantPermission_RejectsUnknownPermissionKey(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner")
	salesRoleID := seedRole(ctx, t, adminPool, firmID, "sales", "Sales", false)

	if err := permission.GrantPermission(ctx, appPool, firmID, ownerUserID, salesRoleID, "no_such_permission_key"); !errors.Is(err, permission.ErrPermissionKeyNotFound) {
		t.Fatalf("expected ErrPermissionKeyNotFound, got: %v", err)
	}
}

func TestRevokePermission_SucceedsAndAudits(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	seedPermissionKey(ctx, t, adminPool, testPermissionKey)

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner")
	salesRoleID := seedRole(ctx, t, adminPool, firmID, "sales", "Sales", false)
	seedGrant(ctx, t, adminPool, firmID, salesRoleID, testPermissionKey)

	if err := permission.RevokePermission(ctx, appPool, firmID, ownerUserID, salesRoleID, testPermissionKey); err != nil {
		t.Fatalf("RevokePermission: %v", err)
	}

	var granted bool
	if err := adminPool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM role_permissions WHERE role_id = $1 AND permission_key = $2)
	`, salesRoleID, testPermissionKey).Scan(&granted); err != nil {
		t.Fatalf("check role_permissions: %v", err)
	}
	if granted {
		t.Error("expected the grant to be gone after revoke")
	}

	var auditCount int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log WHERE entity_type = 'role_permission' AND entity_id = $1 AND action = 'revoke'
	`, salesRoleID).Scan(&auditCount); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("expected 1 'revoke' audit_log row, got %d", auditCount)
	}
}

func TestRevokePermission_RejectsOwnerRoleTarget(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	seedPermissionKey(ctx, t, adminPool, testPermissionKey)

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	seedGrant(ctx, t, adminPool, firmID, ownerRoleID, testPermissionKey)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner")

	if err := permission.RevokePermission(ctx, appPool, firmID, ownerUserID, ownerRoleID, testPermissionKey); !errors.Is(err, permission.ErrOwnerRoleImmutable) {
		t.Fatalf("expected ErrOwnerRoleImmutable, got: %v", err)
	}

	var stillGranted bool
	if err := adminPool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM role_permissions WHERE role_id = $1 AND permission_key = $2)
	`, ownerRoleID, testPermissionKey).Scan(&stillGranted); err != nil {
		t.Fatalf("check role_permissions: %v", err)
	}
	if !stillGranted {
		t.Error("expected the owner role's grant to be untouched")
	}
}
