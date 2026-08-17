// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package budget_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/budget"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise internal/budget against a real Postgres instance -
// RLS isolation and permission enforcement can't be meaningfully faked,
// same convention every other package's integration tests in this
// codebase follow. Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL
// and ZONARYOS_TEST_APP_DATABASE_URL are set.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run budget integration tests")
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
			budgets, budget_lines, accounts, journal_entries, journal_lines,
			audit_log, permissions CASCADE
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

func seedOwner(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmName, keycloakSubject string) (firmID, userID uuid.UUID) {
	t.Helper()

	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ($1) RETURNING id`, firmName).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test Owner").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (firm_id, key, name, is_owner) VALUES ($1, 'owner', 'Owner', true) RETURNING id
		`, firmID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed owner role/membership: %v", err)
	}
	return firmID, userID
}

func seedAccount(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID, code, typ string) uuid.UUID {
	t.Helper()
	var accountID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO accounts (firm_id, code, name, type) VALUES ($1, $2, $2, $3) RETURNING id
	`, firmID, code, typ).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return accountID
}

// TestActivatingBudget_DeactivatesOverlappingActiveBudget confirms only
// one budget can be 'active' per overlapping period: activating a second
// budget whose period overlaps an already-active one flips the first
// back to 'approved', same "flip the others in the same transaction"
// shape internal/manufacturing's BOM active-version enforcement uses.
func TestActivatingBudget_DeactivatesOverlappingActiveBudget(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "budget-active-owner")

	first, err := budget.CreateBudget(ctx, appPool, firmID, ownerID, budget.CreateBudgetInput{
		Name: "2026 Q1", PeriodType: "quarterly", PeriodStart: "2026-01-01", PeriodEnd: "2026-03-31", Status: "active",
	})
	if err != nil {
		t.Fatalf("CreateBudget (first): %v", err)
	}

	second, err := budget.CreateBudget(ctx, appPool, firmID, ownerID, budget.CreateBudgetInput{
		Name: "2026 Q1 Revised", PeriodType: "quarterly", PeriodStart: "2026-02-01", PeriodEnd: "2026-04-30", Status: "active",
	})
	if err != nil {
		t.Fatalf("CreateBudget (second, overlapping): %v", err)
	}

	got, err := budget.GetBudget(ctx, appPool, firmID, ownerID, first.ID)
	if err != nil {
		t.Fatalf("GetBudget (first): %v", err)
	}
	if got.Status != "approved" {
		t.Errorf("expected the first budget to be deactivated to 'approved' once the overlapping second was activated, got %q", got.Status)
	}

	got, err = budget.GetBudget(ctx, appPool, firmID, ownerID, second.ID)
	if err != nil {
		t.Fatalf("GetBudget (second): %v", err)
	}
	if got.Status != "active" {
		t.Errorf("expected the second budget to remain 'active', got %q", got.Status)
	}

	// A non-overlapping active budget must NOT be touched.
	nonOverlapping, err := budget.CreateBudget(ctx, appPool, firmID, ownerID, budget.CreateBudgetInput{
		Name: "2027 Q1", PeriodType: "quarterly", PeriodStart: "2027-01-01", PeriodEnd: "2027-03-31", Status: "active",
	})
	if err != nil {
		t.Fatalf("CreateBudget (non-overlapping): %v", err)
	}
	got, err = budget.GetBudget(ctx, appPool, firmID, ownerID, second.ID)
	if err != nil {
		t.Fatalf("GetBudget (second, after non-overlapping activation): %v", err)
	}
	if got.Status != "active" {
		t.Errorf("expected the second budget to remain 'active' after a non-overlapping budget was activated, got %q", got.Status)
	}
	got, err = budget.GetBudget(ctx, appPool, firmID, ownerID, nonOverlapping.ID)
	if err != nil {
		t.Fatalf("GetBudget (non-overlapping): %v", err)
	}
	if got.Status != "active" {
		t.Errorf("expected the non-overlapping budget to be 'active', got %q", got.Status)
	}
}

// TestApproveBudget_RejectsNonDraft confirms Approve only works from
// 'draft' - a second call (now 'approved') is rejected, not silently
// re-approved.
func TestApproveBudget_RejectsNonDraft(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "budget-approve-owner")

	b, err := budget.CreateBudget(ctx, appPool, firmID, ownerID, budget.CreateBudgetInput{
		Name: "2026 Annual", PeriodType: "annual", PeriodStart: "2026-01-01", PeriodEnd: "2026-12-31",
	})
	if err != nil {
		t.Fatalf("CreateBudget: %v", err)
	}

	approved, err := budget.ApproveBudget(ctx, appPool, firmID, ownerID, b.ID)
	if err != nil {
		t.Fatalf("ApproveBudget: %v", err)
	}
	if approved.Status != "approved" {
		t.Fatalf("expected status 'approved', got %q", approved.Status)
	}
	if approved.ApprovedBy == nil || *approved.ApprovedBy != ownerID {
		t.Errorf("expected approved_by to be set to the caller, got %v", approved.ApprovedBy)
	}

	if _, err := budget.ApproveBudget(ctx, appPool, firmID, ownerID, b.ID); err == nil {
		t.Fatal("expected a second Approve call on an already-approved budget to fail")
	}
}

// TestBudgetLines_RejectDuplicateAndForeignAccount confirms budget line
// creation enforces one line per account and rejects an account_id from
// another firm.
func TestBudgetLines_RejectDuplicateAndForeignAccount(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "budget-lines-owner")
	otherFirmID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm B", "budget-lines-other-owner")

	accountID := seedAccount(ctx, t, adminPool, firmID, "5000", "expense")
	otherFirmAccountID := seedAccount(ctx, t, adminPool, otherFirmID, "5000", "expense")

	b, err := budget.CreateBudget(ctx, appPool, firmID, ownerID, budget.CreateBudgetInput{
		Name: "2026 Annual", PeriodType: "annual", PeriodStart: "2026-01-01", PeriodEnd: "2026-12-31",
	})
	if err != nil {
		t.Fatalf("CreateBudget: %v", err)
	}

	if _, err := budget.CreateBudgetLine(ctx, appPool, firmID, ownerID, b.ID, budget.CreateBudgetLineInput{
		AccountID: accountID, PlannedAmount: "12000.00",
	}); err != nil {
		t.Fatalf("CreateBudgetLine: %v", err)
	}

	if _, err := budget.CreateBudgetLine(ctx, appPool, firmID, ownerID, b.ID, budget.CreateBudgetLineInput{
		AccountID: accountID, PlannedAmount: "500.00",
	}); err == nil {
		t.Fatal("expected a duplicate account_id on the same budget to be rejected")
	}

	if _, err := budget.CreateBudgetLine(ctx, appPool, firmID, ownerID, b.ID, budget.CreateBudgetLineInput{
		AccountID: otherFirmAccountID, PlannedAmount: "500.00",
	}); err == nil {
		t.Fatal("expected an account_id from another firm to be rejected")
	}
}
