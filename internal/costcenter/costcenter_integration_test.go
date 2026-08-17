// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package costcenter_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/costcenter"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise internal/costcenter against a real Postgres
// instance - RLS isolation and permission enforcement can't be
// meaningfully faked, same convention every other package's integration
// tests in this codebase follow. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run cost center integration tests")
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
			cost_centers, accounts, journal_entries, journal_lines,
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

// TestCreateCostCenter_RejectsDuplicateCodeAndForeignParent confirms the
// UNIQUE (firm_id, code) constraint surfaces as a clean error, and a
// parent_id from another firm is rejected.
func TestCreateCostCenter_RejectsDuplicateCodeAndForeignParent(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "cc-owner")
	otherFirmID, otherOwnerID := seedOwner(ctx, t, adminPool, appPool, "Firm B", "cc-other-owner")

	if _, err := costcenter.CreateCostCenter(ctx, appPool, firmID, ownerID, costcenter.CreateCostCenterInput{Code: "OPS", Name: "Operations"}); err != nil {
		t.Fatalf("CreateCostCenter: %v", err)
	}
	if _, err := costcenter.CreateCostCenter(ctx, appPool, firmID, ownerID, costcenter.CreateCostCenterInput{Code: "OPS", Name: "Operations 2"}); err == nil {
		t.Fatal("expected a duplicate code to be rejected")
	}

	otherFirmCC, err := costcenter.CreateCostCenter(ctx, appPool, otherFirmID, otherOwnerID, costcenter.CreateCostCenterInput{Code: "SALES", Name: "Sales"})
	if err != nil {
		t.Fatalf("CreateCostCenter (other firm): %v", err)
	}
	if _, err := costcenter.CreateCostCenter(ctx, appPool, firmID, ownerID, costcenter.CreateCostCenterInput{
		Code: "SUB", Name: "Sub", ParentID: &otherFirmCC.ID,
	}); err == nil {
		t.Fatal("expected a parent_id from another firm to be rejected")
	}
}

// TestGetCostCenterPnL_GroupsCorrectly confirms revenue/expense journal
// lines are grouped and summed correctly per cost center, and that lines
// with no cost_center_id at all land in the "Unassigned" bucket.
func TestGetCostCenterPnL_GroupsCorrectly(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "cc-pnl-owner")

	ops, err := costcenter.CreateCostCenter(ctx, appPool, firmID, ownerID, costcenter.CreateCostCenterInput{Code: "OPS", Name: "Operations"})
	if err != nil {
		t.Fatalf("CreateCostCenter (ops): %v", err)
	}
	sales, err := costcenter.CreateCostCenter(ctx, appPool, firmID, ownerID, costcenter.CreateCostCenterInput{Code: "SALES", Name: "Sales"})
	if err != nil {
		t.Fatalf("CreateCostCenter (sales): %v", err)
	}

	var revenueAccountID, expenseAccountID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO accounts (firm_id, code, name, type) VALUES ($1, '4000', 'Revenue', 'revenue') RETURNING id`, firmID).Scan(&revenueAccountID); err != nil {
		t.Fatalf("seed revenue account: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `INSERT INTO accounts (firm_id, code, name, type) VALUES ($1, '5000', 'Expense', 'expense') RETURNING id`, firmID).Scan(&expenseAccountID); err != nil {
		t.Fatalf("seed expense account: %v", err)
	}

	// Sales: 1000 revenue (credit), tagged to the Sales cost center; its
	// offsetting expense debit is deliberately left untagged, so it lands
	// in the Unassigned bucket - a single journal entry can span cost
	// centers (or none at all) per line, it's not an entry-level property.
	// Ops: 300 expense (debit), 300 revenue (credit), both tagged Ops.
	// One entry with no cost center at all on either line (Unassigned):
	// 50 expense (debit), 50 revenue (credit).
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := accounting.PostJournalEntryTx(ctx, tx, firmID, ownerID, "Sale", "test", nil, []accounting.LineInput{
			{AccountCode: "4000", Side: accounting.SideCredit, Amount: "1000.00", CostCenterID: &sales.ID},
			{AccountCode: "5000", Side: accounting.SideDebit, Amount: "1000.00"},
		})
		return err
	})
	if err != nil {
		t.Fatalf("post sale entry: %v", err)
	}
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := accounting.PostJournalEntryTx(ctx, tx, firmID, ownerID, "Ops expense", "test", nil, []accounting.LineInput{
			{AccountCode: "5000", Side: accounting.SideDebit, Amount: "300.00", CostCenterID: &ops.ID},
			{AccountCode: "4000", Side: accounting.SideCredit, Amount: "300.00", CostCenterID: &ops.ID},
		})
		return err
	})
	if err != nil {
		t.Fatalf("post ops entry: %v", err)
	}
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := accounting.PostJournalEntryTx(ctx, tx, firmID, ownerID, "Unassigned expense", "test", nil, []accounting.LineInput{
			{AccountCode: "5000", Side: accounting.SideDebit, Amount: "50.00"},
			{AccountCode: "4000", Side: accounting.SideCredit, Amount: "50.00"},
		})
		return err
	})
	if err != nil {
		t.Fatalf("post unassigned entry: %v", err)
	}

	lines, err := costcenter.GetCostCenterPnL(ctx, appPool, firmID, ownerID, nil, nil)
	if err != nil {
		t.Fatalf("GetCostCenterPnL: %v", err)
	}

	byName := make(map[string]costcenter.CostCenterPnLLine, len(lines))
	for _, l := range lines {
		byName[l.Name] = l
	}

	salesLine, ok := byName["Sales"]
	if !ok {
		t.Fatal("expected a Sales line")
	}
	if salesLine.Revenue != "1000.0000" {
		t.Errorf("expected Sales revenue 1000.0000, got %q", salesLine.Revenue)
	}
	if salesLine.Expenses != "0.0000" {
		t.Errorf("expected Sales expenses 0.0000, got %q", salesLine.Expenses)
	}

	opsLine, ok := byName["Operations"]
	if !ok {
		t.Fatal("expected an Operations line")
	}
	if opsLine.Expenses != "300.0000" {
		t.Errorf("expected Operations expenses 300.0000, got %q", opsLine.Expenses)
	}
	if opsLine.Revenue != "300.0000" {
		t.Errorf("expected Operations revenue 300.0000, got %q", opsLine.Revenue)
	}

	// The Sale entry's untagged expense debit (1000) plus the fully
	// untagged entry's debit (50).
	unassignedLine, ok := byName["Unassigned"]
	if !ok {
		t.Fatal("expected an Unassigned line")
	}
	if unassignedLine.Expenses != "1050.0000" {
		t.Errorf("expected Unassigned expenses 1050.0000, got %q", unassignedLine.Expenses)
	}
	if unassignedLine.Revenue != "50.0000" {
		t.Errorf("expected Unassigned revenue 50.0000, got %q", unassignedLine.Revenue)
	}
	if unassignedLine.CostCenterID != nil {
		t.Errorf("expected the Unassigned line's CostCenterID to be nil, got %v", *unassignedLine.CostCenterID)
	}
}
