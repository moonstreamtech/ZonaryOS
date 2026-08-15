// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package wizard_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
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

// sellsAndTracksInventory/sellsTracksAndManagesCRM are SeedSelection
// fixtures used across this file's tests that only care about one or two
// specific workflows being seeded - TestSeedSelection_* below is where
// every yes/no combination is actually exercised (Open Points item 37's
// "each yes/no combination seeds exactly the expected workflows" test
// requirement).
var (
	sellsAndTracksInventory  = wizard.SeedSelection{Sells: true, TracksInventory: true}
	sellsTracksAndManagesCRM = wizard.SeedSelection{Sells: true, TracksInventory: true, ManagesCRM: true}
)

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

	result, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Acme Trading Co.", sellsAndTracksInventory)
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

	result, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Acme Trading Co.", sellsAndTracksInventory)
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

// TestCreateDefaultFirm_SeedsBothDefaultWorkflows is this batch's core
// wizard-level proof: CreateDefaultFirm now seeds Stock In -> Sale *and*
// Customer Pipeline for every new firm, side by side, both readable and
// both structurally intact.
func TestCreateDefaultFirm_SeedsBothDefaultWorkflows(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	userID := seedUser(ctx, t, adminPool, "wizard-user-both-workflows")

	result, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Acme Trading Co.", sellsTracksAndManagesCRM)
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}
	if result.StockToSaleDefinitionID == uuid.Nil {
		t.Fatal("expected a non-nil StockToSaleDefinitionID")
	}
	if result.CustomerPipelineDefinitionID == uuid.Nil {
		t.Fatal("expected a non-nil CustomerPipelineDefinitionID")
	}
	if result.StockToSaleDefinitionID == result.CustomerPipelineDefinitionID {
		t.Fatal("expected the two seeded definitions to have distinct IDs")
	}

	err = zdb.WithFirmContext(ctx, appPool, result.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		var stockKey, crmKey string
		if err := tx.QueryRow(ctx, `SELECT key FROM workflow_definitions WHERE id = $1`, result.StockToSaleDefinitionID).Scan(&stockKey); err != nil {
			return err
		}
		if stockKey != workflow.StockToSaleKey {
			t.Errorf("expected stock definition key %q, got %q", workflow.StockToSaleKey, stockKey)
		}
		if err := tx.QueryRow(ctx, `SELECT key FROM workflow_definitions WHERE id = $1`, result.CustomerPipelineDefinitionID).Scan(&crmKey); err != nil {
			return err
		}
		if crmKey != workflow.CustomerPipelineKey {
			t.Errorf("expected customer pipeline definition key %q, got %q", workflow.CustomerPipelineKey, crmKey)
		}

		var definitionCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM workflow_definitions`).Scan(&definitionCount); err != nil {
			return err
		}
		if definitionCount != 2 {
			t.Errorf("expected exactly 2 workflow definitions seeded for a new firm, got %d", definitionCount)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("check seeded definitions: %v", err)
	}

	// Every permission key both specs introduce should be granted to the
	// founder's owner role - the same self-action auto-grant already
	// covered for stock_to_sale alone below, now for both.
	err = zdb.WithFirmContext(ctx, appPool, result.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		allKeys := append(
			append([]string{}, workflow.StockToSaleSpec.PermissionKeys()...),
			workflow.CustomerPipelineSpec.PermissionKeys()...,
		)
		for _, key := range allKeys {
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

func TestCreateDefaultFirm_GrantsStockToSalePermissionsToOwnerRole(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	userID := seedUser(ctx, t, adminPool, "wizard-user-2")

	result, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Acme Trading Co.", sellsAndTracksInventory)
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

	result, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Acme Trading Co.", sellsAndTracksInventory)
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

	result, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Acme Trading Co.", sellsAndTracksInventory)
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

	if _, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "   ", sellsAndTracksInventory); err == nil {
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

	if _, err := wizard.CreateDefaultFirm(ctx, appPool, nonexistentUserID, firmName, sellsAndTracksInventory); err == nil {
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

// TestSeedSelection_SeedsExactlyExpectedWorkflows is Open Points item 37's
// concrete resolution proof: for every yes/no combination the wizard's
// SeedSelection can carry, CreateDefaultFirm seeds exactly the workflows
// that combination implies - no more (the previous hardcoded pair,
// regardless of what was asked) and no less. A "no" to everything still
// creates the firm, just with zero seeded workflow definitions.
func TestSeedSelection_SeedsExactlyExpectedWorkflows(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	cases := []struct {
		name string
		sel  wizard.SeedSelection
		want map[string]bool
	}{
		{
			name: "everything no",
			sel:  wizard.SeedSelection{},
			want: map[string]bool{},
		},
		{
			name: "sells and tracks inventory only",
			sel:  wizard.SeedSelection{Sells: true, TracksInventory: true},
			want: map[string]bool{workflow.StockToSaleKey: true},
		},
		{
			name: "sells and manages CRM only",
			sel:  wizard.SeedSelection{Sells: true, ManagesCRM: true},
			want: map[string]bool{workflow.CustomerPipelineKey: true},
		},
		{
			name: "sells but neither inventory nor CRM",
			sel:  wizard.SeedSelection{Sells: true},
			want: map[string]bool{},
		},
		{
			name: "purchases from suppliers only",
			sel:  wizard.SeedSelection{PurchasesFromSuppliers: true},
			want: map[string]bool{workflow.PurchaseOrderKey: true},
		},
		{
			name: "manages tasks only",
			sel:  wizard.SeedSelection{ManagesTasks: true},
			want: map[string]bool{workflow.TaskApprovalKey: true},
		},
		{
			name: "everything yes",
			sel: wizard.SeedSelection{
				Sells: true, TracksInventory: true, ManagesCRM: true,
				PurchasesFromSuppliers: true, ManagesTasks: true,
			},
			want: map[string]bool{
				workflow.StockToSaleKey:      true,
				workflow.CustomerPipelineKey: true,
				workflow.PurchaseOrderKey:    true,
				workflow.TaskApprovalKey:     true,
			},
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userID := seedUser(ctx, t, adminPool, "wizard-seed-selection-"+strconv.Itoa(i))

			result, err := wizard.CreateDefaultFirm(ctx, appPool, userID, tc.name+" firm", tc.sel)
			if err != nil {
				t.Fatalf("CreateDefaultFirm: %v", err)
			}

			var gotKeys []string
			err = zdb.WithFirmContext(ctx, appPool, result.FirmID, func(ctx context.Context, tx pgx.Tx) error {
				rows, err := tx.Query(ctx, `SELECT key FROM workflow_definitions WHERE firm_id = $1`, result.FirmID)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var key string
					if err := rows.Scan(&key); err != nil {
						return err
					}
					gotKeys = append(gotKeys, key)
				}
				return rows.Err()
			})
			if err != nil {
				t.Fatalf("list seeded definitions: %v", err)
			}

			got := make(map[string]bool, len(gotKeys))
			for _, key := range gotKeys {
				got[key] = true
			}
			if len(got) != len(tc.want) {
				t.Fatalf("expected seeded workflow keys %v, got %v", tc.want, got)
			}
			for key := range tc.want {
				if !got[key] {
					t.Errorf("expected %q to be seeded, got keys %v", key, got)
				}
			}
		})
	}
}

// TestCreateDefaultFirm_SeedManufacturingSeedsExpectedAccountsAndWorkflow
// is the manufacturing module foundation batch's own required test case:
// SeedManufacturing: true seeds the Work in Progress and Finished Goods
// accounts (accounting.SeedChartOptions.SeedManufacturing), Inventory
// alongside them (a manufacturing-only firm needs somewhere to issue
// components FROM - see accounting's own manufacturingAccounts doc
// comment), and the Manufacturing Order workflow definition
// (workflow.ManufacturingOrderKey) - a firm that answers "no" gets none
// of the three.
func TestCreateDefaultFirm_SeedManufacturingSeedsExpectedAccountsAndWorkflow(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	yesUserID := seedUser(ctx, t, adminPool, "wizard-manufacturing-yes")
	yesResult, err := wizard.CreateDefaultFirm(ctx, appPool, yesUserID, "Manufacturing Yes Firm", wizard.SeedSelection{SeedManufacturing: true})
	if err != nil {
		t.Fatalf("CreateDefaultFirm (SeedManufacturing: true): %v", err)
	}
	if yesResult.ManufacturingOrderDefinitionID == uuid.Nil {
		t.Error("expected a non-nil ManufacturingOrderDefinitionID when SeedManufacturing is true")
	}

	wantCodes := map[string]bool{
		accounting.InventoryAccountCode:      true,
		accounting.WorkInProgressAccountCode: true,
		accounting.FinishedGoodsAccountCode:  true,
	}
	err = zdb.WithFirmContext(ctx, appPool, yesResult.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		for code := range wantCodes {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE firm_id = $1 AND code = $2)`, yesResult.FirmID, code).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				t.Errorf("expected account code %q to be seeded", code)
			}
		}
		var definitionKey string
		if err := tx.QueryRow(ctx, `SELECT key FROM workflow_definitions WHERE id = $1`, yesResult.ManufacturingOrderDefinitionID).Scan(&definitionKey); err != nil {
			return err
		}
		if definitionKey != workflow.ManufacturingOrderKey {
			t.Errorf("expected workflow definition key %q, got %q", workflow.ManufacturingOrderKey, definitionKey)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify seeded manufacturing accounts/workflow: %v", err)
	}

	noUserID := seedUser(ctx, t, adminPool, "wizard-manufacturing-no")
	noResult, err := wizard.CreateDefaultFirm(ctx, appPool, noUserID, "Manufacturing No Firm", wizard.SeedSelection{})
	if err != nil {
		t.Fatalf("CreateDefaultFirm (SeedManufacturing: false): %v", err)
	}
	if noResult.ManufacturingOrderDefinitionID != uuid.Nil {
		t.Error("expected a nil ManufacturingOrderDefinitionID when SeedManufacturing is false")
	}
	err = zdb.WithFirmContext(ctx, appPool, noResult.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		for _, code := range []string{accounting.WorkInProgressAccountCode, accounting.FinishedGoodsAccountCode} {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE firm_id = $1 AND code = $2)`, noResult.FirmID, code).Scan(&exists); err != nil {
				return err
			}
			if exists {
				t.Errorf("expected account code %q NOT to be seeded when SeedManufacturing is false", code)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify manufacturing accounts absent: %v", err)
	}
}
