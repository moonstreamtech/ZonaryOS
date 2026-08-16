// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package asset_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/asset"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// These tests exercise internal/asset against a real Postgres instance -
// RLS isolation and permission enforcement can't be meaningfully faked,
// same convention every other package's integration tests in this
// codebase follow. Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL
// and ZONARYOS_TEST_APP_DATABASE_URL are set.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run asset integration tests")
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
			assets, maintenance_schedules, maintenance_records, notifications,
			accounts, journal_entries, journal_lines, people,
			workflow_definitions, workflow_states, workflow_transitions, workflow_instances,
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

func seedOwner(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmName, keycloakSubject string) (firmID, userID, roleID uuid.UUID) {
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
	return firmID, userID, roleID
}

func seedCoreAccounts(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID uuid.UUID) {
	t.Helper()
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return accounting.SeedDefaultChartOfAccountsTx(ctx, tx, firmID, accounting.SeedChartOptions{})
	})
	if err != nil {
		t.Fatalf("seed core accounts: %v", err)
	}
}

func seedPerson(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID) uuid.UUID {
	t.Helper()
	var personID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO people (firm_id, full_name, type) VALUES ($1, 'Test Person', 'employee') RETURNING id
	`, firmID).Scan(&personID); err != nil {
		t.Fatalf("seed person: %v", err)
	}
	return personID
}

// TestCreateMaintenanceRecord_AdvancesScheduleAndRecomputesNextDueAt
// confirms that logging a maintenance record against a schedule advances
// last_done_at, and that next_due_at (a GENERATED ALWAYS column) recomputes
// automatically from the new last_done_at + interval_days - this function
// never computes or writes next_due_at itself.
func TestCreateMaintenanceRecord_AdvancesScheduleAndRecomputesNextDueAt(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm A", "maint-owner")

	a, err := asset.CreateAsset(ctx, appPool, firmID, ownerID, asset.CreateAssetInput{
		Name: "Forklift 1", AssetNumber: "AST-001", Type: "equipment",
	})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}

	sched, err := asset.CreateMaintenanceSchedule(ctx, appPool, firmID, ownerID, a.ID, asset.CreateMaintenanceScheduleInput{
		Name: "Oil change", IntervalDays: 30, LastDoneAt: "2025-01-01",
	})
	if err != nil {
		t.Fatalf("CreateMaintenanceSchedule: %v", err)
	}
	if sched.NextDueAt == nil || sched.NextDueAt.Format("2006-01-02") != "2025-01-31" {
		t.Fatalf("expected initial next_due_at 2025-01-31, got %v", sched.NextDueAt)
	}

	rec, err := asset.CreateMaintenanceRecord(ctx, appPool, firmID, ownerID, a.ID, asset.CreateMaintenanceRecordInput{
		ScheduleID: &sched.ID, PerformedAt: "2025-02-15", Description: "Changed oil and filter",
	})
	if err != nil {
		t.Fatalf("CreateMaintenanceRecord: %v", err)
	}
	if rec.PerformedAt.Format("2006-01-02") != "2025-02-15" {
		t.Fatalf("expected record performed_at 2025-02-15, got %v", rec.PerformedAt)
	}

	schedules, err := asset.ListMaintenanceSchedules(ctx, appPool, firmID, ownerID, a.ID)
	if err != nil {
		t.Fatalf("ListMaintenanceSchedules: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("expected 1 schedule, got %d", len(schedules))
	}
	got := schedules[0]
	if got.LastDoneAt == nil || got.LastDoneAt.Format("2006-01-02") != "2025-02-15" {
		t.Fatalf("expected last_done_at advanced to 2025-02-15, got %v", got.LastDoneAt)
	}
	if got.NextDueAt == nil || got.NextDueAt.Format("2006-01-02") != "2025-03-17" {
		t.Fatalf("expected next_due_at recomputed to 2025-03-17 (2025-02-15 + 30 days), got %v", got.NextDueAt)
	}
}

// TestCreateMaintenanceRecord_PostsJournalEntryWhenCostProvided confirms a
// non-zero cost posts a real, balanced journal entry: DR Maintenance
// Expense / CR Cash (the firm has a Cash account seeded, per
// seedCoreAccounts).
func TestCreateMaintenanceRecord_PostsJournalEntryWhenCostProvided(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm A", "maint-cost-owner")
	seedCoreAccounts(ctx, t, appPool, firmID)

	a, err := asset.CreateAsset(ctx, appPool, firmID, ownerID, asset.CreateAssetInput{
		Name: "Delivery Van", AssetNumber: "AST-002", Type: "vehicle",
	})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}

	cost := "150.00"
	rec, err := asset.CreateMaintenanceRecord(ctx, appPool, firmID, ownerID, a.ID, asset.CreateMaintenanceRecordInput{
		PerformedAt: "2025-03-01", Description: "Brake pad replacement", Cost: &cost,
	})
	if err != nil {
		t.Fatalf("CreateMaintenanceRecord: %v", err)
	}

	var debitAccount, creditAccount, debitAmount, creditAmount string
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT
				(SELECT a.code FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id WHERE jl.entry_id = je.id AND jl.side = 'debit'),
				(SELECT jl.amount::text FROM journal_lines jl WHERE jl.entry_id = je.id AND jl.side = 'debit'),
				(SELECT a.code FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id WHERE jl.entry_id = je.id AND jl.side = 'credit'),
				(SELECT jl.amount::text FROM journal_lines jl WHERE jl.entry_id = je.id AND jl.side = 'credit')
			FROM journal_entries je WHERE je.firm_id = $1 AND je.source_type = 'maintenance_record' AND je.source_id = $2
		`, firmID, rec.ID).Scan(&debitAccount, &debitAmount, &creditAccount, &creditAmount)
	})
	if err != nil {
		t.Fatalf("look up posted journal entry: %v", err)
	}
	if debitAccount != accounting.MaintenanceExpenseAccountCode {
		t.Errorf("expected debit account %q (Maintenance Expense), got %q", accounting.MaintenanceExpenseAccountCode, debitAccount)
	}
	if creditAccount != accounting.CashAccountCode {
		t.Errorf("expected credit account %q (Cash), got %q", accounting.CashAccountCode, creditAccount)
	}
	if debitAmount != "150.0000" || creditAmount != "150.0000" {
		t.Errorf("expected both lines to be 150.0000, got debit=%q credit=%q", debitAmount, creditAmount)
	}
}

// TestCreateMaintenanceRecord_NoCostNoJournalEntry confirms a maintenance
// record with no cost posts nothing to the ledger - the whole point of
// the "if cost is provided" condition.
func TestCreateMaintenanceRecord_NoCostNoJournalEntry(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm A", "maint-nocost-owner")
	seedCoreAccounts(ctx, t, appPool, firmID)

	a, err := asset.CreateAsset(ctx, appPool, firmID, ownerID, asset.CreateAssetInput{
		Name: "Ladder", AssetNumber: "AST-003", Type: "tool",
	})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}

	rec, err := asset.CreateMaintenanceRecord(ctx, appPool, firmID, ownerID, a.ID, asset.CreateMaintenanceRecordInput{
		PerformedAt: "2025-03-01", Description: "Visual inspection, no cost",
	})
	if err != nil {
		t.Fatalf("CreateMaintenanceRecord: %v", err)
	}

	var count int
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE firm_id = $1 AND source_type = 'maintenance_record' AND source_id = $2`, firmID, rec.ID).Scan(&count)
	})
	if err != nil {
		t.Fatalf("count journal entries: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no journal entry posted for a costless maintenance record, got %d", count)
	}
}

// TestProcessDueMaintenanceSchedules_NotifiesOwnersWhenDue confirms the
// background scheduler's testable core (ProcessDueMaintenanceSchedules)
// finds an active schedule whose next_due_at is already within the
// lookahead window (well within 1 day - a schedule already overdue) and
// creates a real notification for the firm's owner, without needing to
// wait for a real ticker tick or the production 7-day lookahead.
func TestProcessDueMaintenanceSchedules_NotifiesOwnersWhenDue(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm A", "scheduler-owner")
	personID := seedPerson(ctx, t, adminPool, firmID)

	a, err := asset.CreateAsset(ctx, appPool, firmID, ownerID, asset.CreateAssetInput{
		Name: "Generator", AssetNumber: "AST-004", Type: "equipment",
	})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}

	// last_done_at far enough in the past that next_due_at is already
	// overdue - well within any lookahead window, including a 1-day one -
	// without waiting real time for it to become due.
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	sched, err := asset.CreateMaintenanceSchedule(ctx, appPool, firmID, ownerID, a.ID, asset.CreateMaintenanceScheduleInput{
		Name: "Generator service", IntervalDays: 1, LastDoneAt: yesterday, AssignedToPersonID: &personID,
	})
	if err != nil {
		t.Fatalf("CreateMaintenanceSchedule: %v", err)
	}

	asset.ProcessDueMaintenanceSchedules(ctx, appPool)

	var count int
	var payloadScheduleID string
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM notifications WHERE firm_id = $1 AND recipient_user_id = $2 AND type = 'maintenance_due'
		`, firmID, ownerID).Scan(&count); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT payload->>'scheduleId' FROM notifications WHERE firm_id = $1 AND type = 'maintenance_due' LIMIT 1
		`, firmID).Scan(&payloadScheduleID)
	})
	if err != nil {
		t.Fatalf("look up notification: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 maintenance_due notification for the owner, got %d", count)
	}
	if payloadScheduleID != sched.ID.String() {
		t.Errorf("expected notification payload to reference schedule %s, got %q", sched.ID, payloadScheduleID)
	}

	// Running the same poll again the same day must NOT create a second
	// notification for the same schedule (alreadyNotifiedTodayTx).
	asset.ProcessDueMaintenanceSchedules(ctx, appPool)
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE firm_id = $1 AND type = 'maintenance_due'`, firmID).Scan(&count)
	})
	if err != nil {
		t.Fatalf("re-count notifications: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected still exactly 1 notification after a second same-day poll (dedup), got %d", count)
	}
}

// TestFieldTypeAsset_ValidatesAssetReference confirms a workflow field of
// type "asset" rejects a nonexistent asset ID and accepts a real one -
// internal/workflow's own assetValidator, exercised end-to-end through a
// minimal ad hoc workflow definition (same pattern
// internal/project's own integration test uses for FieldTypeProject via
// task_approval's project_id field, but this defines its own definition
// directly since no seeded spec carries an asset field).
func TestFieldTypeAsset_ValidatesAssetReference(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm A", "field-asset-owner")

	a, err := asset.CreateAsset(ctx, appPool, firmID, ownerID, asset.CreateAssetInput{
		Name: "Truck", AssetNumber: "AST-005", Type: "vehicle",
	})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}

	spec := workflow.DefinitionSpec{
		Key: "asset_repair", Name: "Asset Repair", CreatePermission: workflow.PermissionSpec{Key: "workflow.asset_repair.create"},
		Fields: []workflow.FieldSpec{{Name: "asset_id", Type: workflow.FieldTypeAsset, Required: true}},
		States: []workflow.StateSpec{
			{Key: "open", Name: "Open", IsInitial: true},
			{Key: "resolved", Name: "Resolved", IsTerminal: true},
		},
		Transitions: []workflow.TransitionSpec{{
			ActionKey: "resolve", Name: "Resolve", FromStateKey: "open", ToStateKey: "resolved",
			Permission: workflow.PermissionSpec{Key: "workflow.asset_repair.resolve"},
		}},
	}
	defID, err := workflow.DefineWorkflowForFirm(ctx, appPool, firmID, ownerID, spec)
	if err != nil {
		t.Fatalf("DefineWorkflowForFirm: %v", err)
	}

	if _, err := workflow.CreateInstance(ctx, appPool, firmID, ownerID, defID, map[string]any{"asset_id": uuid.New().String()}); err == nil {
		t.Fatal("expected CreateInstance to reject a nonexistent asset_id")
	}

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, ownerID, defID, map[string]any{"asset_id": a.ID.String()})
	if err != nil {
		t.Fatalf("expected CreateInstance to accept a real asset_id: %v", err)
	}
	if instanceID == uuid.Nil {
		t.Fatal("expected a real instance id")
	}
}
