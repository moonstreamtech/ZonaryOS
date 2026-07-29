// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package wizard_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/wizard"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// These tests exercise the wizard's firm-creation transaction against a
// real Postgres instance - RLS isolation can't be meaningfully faked,
// same convention as internal/workflow's and internal/platform/db's
// integration tests. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run wizard integration tests")
	}
	return adminDSN, appDSN
}

func setupTest(t *testing.T) (adminPool, appPool *pgxpool.Pool) {
	t.Helper()
	adminDSN, appDSN := testPools(t)
	ctx := context.Background()

	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	adminPool, err := zdb.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)
	if _, err := adminPool.Exec(ctx, `
		TRUNCATE firms, users, roles, role_permissions, user_firm_roles,
			workflow_definitions, workflow_states, workflow_transitions,
			workflow_instances, audit_log, permissions CASCADE
	`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err = zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(appPool.Close)

	return adminPool, appPool
}

func seedUser(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, keycloakSubject string) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test User").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return userID
}

func TestCreateDefaultFirm_CreatesFirmRoleMembershipAndWorkflow(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	userID := seedUser(ctx, t, adminPool, "wizard-user-1")

	result, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Acme Trading Co.")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}
	if result.FirmID == uuid.Nil {
		t.Fatal("expected a non-nil firm ID")
	}
	if result.FirmName != "Acme Trading Co." {
		t.Errorf("expected firm name %q, got %q", "Acme Trading Co.", result.FirmName)
	}

	// The user should now see exactly this one firm via the same
	// discovery mechanism PR 3 built (internal/identity.Memberships).
	memberships, err := identity.Memberships(ctx, appPool, userID)
	if err != nil {
		t.Fatalf("Memberships: %v", err)
	}
	if len(memberships) != 1 {
		t.Fatalf("expected exactly 1 membership, got %d", len(memberships))
	}
	if memberships[0].FirmID != result.FirmID {
		t.Errorf("expected membership firm ID %v, got %v", result.FirmID, memberships[0].FirmID)
	}

	role, err := identity.RoleInFirm(ctx, appPool, result.FirmID, userID)
	if err != nil {
		t.Fatalf("RoleInFirm: %v", err)
	}
	if role.RoleKey != "owner" {
		t.Errorf("expected default role key %q, got %q", "owner", role.RoleKey)
	}
}

// TestCreateDefaultFirm_FlagsOwnerRoleAsOwner covers migrations/
// 0004_role_owner_flag.up.sql's is_owner column: CreateDefaultFirm's
// default role should be flagged so a future Permission Audit Mode UI can
// exclude it from grant/revoke lists (Vision §3) - this flag is cosmetic
// only and never consulted by permission.Has, which the other tests in
// this file already exercise indirectly by relying on real
// role_permissions grants.
func TestCreateDefaultFirm_FlagsOwnerRoleAsOwner(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	userID := seedUser(ctx, t, adminPool, "wizard-user-owner-flag")

	result, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Acme Trading Co.")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	var isOwner bool
	if err := adminPool.QueryRow(ctx, `SELECT is_owner FROM roles WHERE id = $1`, result.RoleID).Scan(&isOwner); err != nil {
		t.Fatalf("query is_owner: %v", err)
	}
	if !isOwner {
		t.Error("expected the default role to be flagged is_owner")
	}
}

func TestCreateDefaultFirm_GrantsStockToSalePermissionsToOwnerRole(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	userID := seedUser(ctx, t, adminPool, "wizard-user-2")

	result, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Acme Trading Co.")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	err = zdb.WithFirmContext(ctx, appPool, result.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		for _, key := range workflow.StockToSaleSpec.PermissionKeys() {
			var granted bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM role_permissions WHERE firm_id = $1 AND role_id = $2 AND permission_key = $3
				)
			`, result.FirmID, result.RoleID, key).Scan(&granted); err != nil {
				return err
			}
			if !granted {
				t.Errorf("expected owner role to be granted permission %q", key)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("check role_permissions: %v", err)
	}
}

func TestCreateDefaultFirm_SeedsStockToSaleWorkflow(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	userID := seedUser(ctx, t, adminPool, "wizard-user-3")

	result, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Acme Trading Co.")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	err = zdb.WithFirmContext(ctx, appPool, result.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		var definitionKey string
		return tx.QueryRow(ctx, `
			SELECT key FROM workflow_definitions WHERE id = $1
		`, result.StockToSaleDefinitionID).Scan(&definitionKey)
	})
	if err != nil {
		t.Fatalf("expected the seeded workflow definition to be readable: %v", err)
	}
}

func TestCreateDefaultFirm_WritesAuditLogEntry(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	userID := seedUser(ctx, t, adminPool, "wizard-user-4")

	result, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Acme Trading Co.")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	err = zdb.WithFirmContext(ctx, appPool, result.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM audit_log
			WHERE firm_id = $1 AND entity_type = 'firm' AND entity_id = $1 AND action = 'create'
		`, result.FirmID).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("expected exactly 1 firm-creation audit_log row, got %d", count)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("check audit_log: %v", err)
	}
}

func TestCreateDefaultFirm_RejectsEmptyFirmName(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	userID := seedUser(ctx, t, adminPool, "wizard-user-5")

	if _, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "   "); err == nil {
		t.Fatal("expected an error for a blank firm name, got nil")
	}
}

// TestCreateDefaultFirm_RollsBackFullyOnFailure forces a failure partway
// through the transaction (a nonexistent user ID violates
// user_firm_roles' foreign key, which is only reached after the firm,
// role, workflow and role_permissions rows have already been written to
// the same transaction) and asserts nothing survives - proving this is
// genuinely one atomic operation, not a sequence of independently
// committed steps a caller could observe half-done.
func TestCreateDefaultFirm_RollsBackFullyOnFailure(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	nonexistentUserID := uuid.New()
	const firmName = "Should Not Persist Co."

	if _, err := wizard.CreateDefaultFirm(ctx, appPool, nonexistentUserID, firmName); err == nil {
		t.Fatal("expected CreateDefaultFirm to fail for a user that doesn't exist, got nil")
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM firms WHERE name = $1`, firmName).Scan(&count); err != nil {
		t.Fatalf("count firms: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no firm row to survive the rolled-back transaction, found %d", count)
	}
}
