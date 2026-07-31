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

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
)

// These tests cover item 5's role CRUD (CreateRole/RenameRole/DeleteRole/
// ListMembers): the owner-only gate, the is_owner-role protection
// (RenameRole/DeleteRole must never touch the structural owner role), and
// the delete-blocked-while-members-exist decision (ErrRoleHasMembers, not
// a silent cascade-unassign - see roles.go's DeleteRole doc comment).

func TestCreateRole_OwnerCanCreateNonOwnerRole(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner-create-role")

	roleID, err := permission.CreateRole(ctx, appPool, firmID, ownerUserID, "sales", "Sales")
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if roleID == uuid.Nil {
		t.Fatal("expected a real role ID back")
	}

	var key, name string
	var isOwner bool
	if err := adminPool.QueryRow(ctx, `SELECT key, name, is_owner FROM roles WHERE id = $1`, roleID).Scan(&key, &name, &isOwner); err != nil {
		t.Fatalf("look up created role: %v", err)
	}
	if key != "sales" || name != "Sales" {
		t.Errorf("expected role key/name to be sales/Sales, got %q/%q", key, name)
	}
	if isOwner {
		t.Error("expected the new role to not be owner-flagged")
	}

	// It must start with zero grants - GetFirmPermissionAudit's LEFT
	// JOIN against role_permissions is what item 2's UI relies on to
	// show that state, not a separate code path.
	audit, err := permission.GetFirmPermissionAudit(ctx, appPool, firmID, ownerUserID)
	if err != nil {
		t.Fatalf("GetFirmPermissionAudit: %v", err)
	}
	found := false
	for _, r := range audit.Roles {
		if r.RoleID == roleID {
			found = true
			if len(r.PermissionKeys) != 0 {
				t.Errorf("expected the new role to start with zero grants, got %v", r.PermissionKeys)
			}
		}
	}
	if !found {
		t.Fatal("expected the newly created role to appear in GetFirmPermissionAudit's Roles")
	}
}

func TestCreateRole_RequiresOwner(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	nonOwnerRoleID := seedRole(ctx, t, adminPool, firmID, "sales", "Sales", false)
	nonOwnerUserID := seedUserWithRole(ctx, t, adminPool, firmID, nonOwnerRoleID, "sub-non-owner-create-role")

	if _, err := permission.CreateRole(ctx, appPool, firmID, nonOwnerUserID, "warehouse", "Warehouse"); !errors.Is(err, permission.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner, got: %v", err)
	}
}

func TestCreateRole_RejectsDuplicateKeyAndEmptyFields(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner-dup-role")

	if _, err := permission.CreateRole(ctx, appPool, firmID, ownerUserID, "sales", "Sales"); err != nil {
		t.Fatalf("first CreateRole: %v", err)
	}
	if _, err := permission.CreateRole(ctx, appPool, firmID, ownerUserID, "sales", "Sales Again"); !errors.Is(err, permission.ErrRoleKeyExists) {
		t.Fatalf("expected ErrRoleKeyExists on duplicate key, got: %v", err)
	}
	if _, err := permission.CreateRole(ctx, appPool, firmID, ownerUserID, "", "No Key"); !errors.Is(err, permission.ErrInvalidRoleKey) {
		t.Fatalf("expected ErrInvalidRoleKey for an empty key, got: %v", err)
	}
	if _, err := permission.CreateRole(ctx, appPool, firmID, ownerUserID, "no-name", "  "); !errors.Is(err, permission.ErrInvalidRoleName) {
		t.Fatalf("expected ErrInvalidRoleName for a blank name, got: %v", err)
	}
}

func TestRenameRole_OwnerCanRenameNonOwnerRole(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner-rename-role")
	salesRoleID := seedRole(ctx, t, adminPool, firmID, "sales", "Sales", false)

	if err := permission.RenameRole(ctx, appPool, firmID, ownerUserID, salesRoleID, "Sales Team"); err != nil {
		t.Fatalf("RenameRole: %v", err)
	}

	var name string
	if err := adminPool.QueryRow(ctx, `SELECT name FROM roles WHERE id = $1`, salesRoleID).Scan(&name); err != nil {
		t.Fatalf("look up renamed role: %v", err)
	}
	if name != "Sales Team" {
		t.Errorf("expected renamed role name to be 'Sales Team', got %q", name)
	}
}

// TestRenameRole_RejectsOwnerRoleTarget is the is_owner-protection test
// the task explicitly calls for: the owner role can never be renamed
// through this surface, since it's the one structural anchor Permission
// Audit Mode's "roles at the highest permission tier don't appear in
// these lists" exclusion (and everything downstream of is_owner) depends
// on staying what it is.
func TestRenameRole_RejectsOwnerRoleTarget(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner-rename-owner-role")

	if err := permission.RenameRole(ctx, appPool, firmID, ownerUserID, ownerRoleID, "Not Owner Anymore"); !errors.Is(err, permission.ErrOwnerRoleImmutable) {
		t.Fatalf("expected ErrOwnerRoleImmutable when renaming the owner role, got: %v", err)
	}

	var name string
	var isOwner bool
	if err := adminPool.QueryRow(ctx, `SELECT name, is_owner FROM roles WHERE id = $1`, ownerRoleID).Scan(&name, &isOwner); err != nil {
		t.Fatalf("look up owner role: %v", err)
	}
	if name != "Owner" || !isOwner {
		t.Errorf("expected the owner role to be untouched (name=Owner, is_owner=true), got name=%q is_owner=%v", name, isOwner)
	}
}

func TestDeleteRole_OwnerCanDeleteEmptyNonOwnerRole(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner-delete-role")
	salesRoleID := seedRole(ctx, t, adminPool, firmID, "sales", "Sales", false)
	seedPermissionKey(ctx, t, adminPool, testPermissionKey)
	seedGrant(ctx, t, adminPool, firmID, salesRoleID, testPermissionKey)

	if err := permission.DeleteRole(ctx, appPool, firmID, ownerUserID, salesRoleID); err != nil {
		t.Fatalf("DeleteRole: %v", err)
	}

	var exists bool
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM roles WHERE id = $1)`, salesRoleID).Scan(&exists); err != nil {
		t.Fatalf("check role deleted: %v", err)
	}
	if exists {
		t.Error("expected the role to be deleted")
	}

	// role_permissions.role_id is ON DELETE CASCADE - the grant seeded
	// above must be gone too, not orphaned.
	var grantExists bool
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM role_permissions WHERE role_id = $1)`, salesRoleID).Scan(&grantExists); err != nil {
		t.Fatalf("check role_permissions cascade: %v", err)
	}
	if grantExists {
		t.Error("expected the deleted role's grants to be cascade-deleted too")
	}
}

// TestDeleteRole_BlockedWhileMembersExist is the delete-with-members
// decision the task asked to be explicit about: blocked (ErrRoleHasMembers,
// 409), not a silent cascade-unassign - see roles.go's DeleteRole doc
// comment for why.
func TestDeleteRole_BlockedWhileMembersExist(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner-delete-blocked")
	salesRoleID := seedRole(ctx, t, adminPool, firmID, "sales", "Sales", false)
	seedUserWithRole(ctx, t, adminPool, firmID, salesRoleID, "sub-sales-member")

	if err := permission.DeleteRole(ctx, appPool, firmID, ownerUserID, salesRoleID); !errors.Is(err, permission.ErrRoleHasMembers) {
		t.Fatalf("expected ErrRoleHasMembers, got: %v", err)
	}

	var exists bool
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM roles WHERE id = $1)`, salesRoleID).Scan(&exists); err != nil {
		t.Fatalf("check role still exists: %v", err)
	}
	if !exists {
		t.Error("expected the role (and its member) to be untouched by the blocked delete")
	}
}

// TestDeleteRole_RejectsOwnerRoleTarget is the other half of the
// is_owner-protection test: the owner role can never be deleted, even by
// the owner themself, even though it has exactly one member (them).
func TestDeleteRole_RejectsOwnerRoleTarget(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmID, "owner", "Owner", true)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmID, ownerRoleID, "sub-owner-delete-owner-role")

	if err := permission.DeleteRole(ctx, appPool, firmID, ownerUserID, ownerRoleID); !errors.Is(err, permission.ErrOwnerRoleImmutable) {
		t.Fatalf("expected ErrOwnerRoleImmutable when deleting the owner role, got: %v", err)
	}

	var exists bool
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM roles WHERE id = $1)`, ownerRoleID).Scan(&exists); err != nil {
		t.Fatalf("check owner role still exists: %v", err)
	}
	if !exists {
		t.Fatal("expected the owner role to still exist - it must never be deletable")
	}
}

func TestRenameRole_DeleteRole_RequireOwner(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	salesRoleID := seedRole(ctx, t, adminPool, firmID, "sales", "Sales", false)
	nonOwnerUserID := seedUserWithRole(ctx, t, adminPool, firmID, salesRoleID, "sub-non-owner-rename-delete")

	if err := permission.RenameRole(ctx, appPool, firmID, nonOwnerUserID, salesRoleID, "New Name"); !errors.Is(err, permission.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner from RenameRole, got: %v", err)
	}
	if err := permission.DeleteRole(ctx, appPool, firmID, nonOwnerUserID, salesRoleID); !errors.Is(err, permission.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner from DeleteRole, got: %v", err)
	}
}

func TestListMembers_ReturnsEveryMemberAndIsMembershipGated(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	ownerRoleID := seedRole(ctx, t, adminPool, firmA, "owner", "Owner", true)
	ownerUserID := seedUserWithRole(ctx, t, adminPool, firmA, ownerRoleID, "sub-owner-members")
	salesRoleID := seedRole(ctx, t, adminPool, firmA, "sales", "Sales", false)
	salesUserID := seedUserWithRole(ctx, t, adminPool, firmA, salesRoleID, "sub-sales-members")

	members, err := permission.ListMembers(ctx, appPool, firmA, ownerUserID)
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d: %+v", len(members), members)
	}
	byUser := make(map[uuid.UUID]permission.FirmMember, len(members))
	for _, m := range members {
		byUser[m.UserID] = m
	}
	if m, ok := byUser[ownerUserID]; !ok || m.RoleKey != "owner" {
		t.Errorf("expected the owner user to be listed with role key 'owner', got %+v (found=%v)", m, ok)
	}
	if m, ok := byUser[salesUserID]; !ok || m.RoleKey != "sales" {
		t.Errorf("expected the sales user to be listed with role key 'sales', got %+v (found=%v)", m, ok)
	}

	// Non-owner members can see the roster too - membership-gated, not
	// owner-only (see ListMembers's doc comment).
	if _, err := permission.ListMembers(ctx, appPool, firmA, salesUserID); err != nil {
		t.Fatalf("expected a non-owner member to be able to list members, got: %v", err)
	}

	// A genuine non-member is rejected.
	var firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	firmBOwnerRoleID := seedRole(ctx, t, adminPool, firmB, "owner", "Owner", true)
	firmBOwnerUserID := seedUserWithRole(ctx, t, adminPool, firmB, firmBOwnerRoleID, "sub-owner-firm-b")

	if _, err := permission.ListMembers(ctx, appPool, firmA, firmBOwnerUserID); !errors.Is(err, permission.ErrFirmNotFound) {
		t.Fatalf("expected ErrFirmNotFound for a non-member listing firm A's roster, got: %v", err)
	}
}
