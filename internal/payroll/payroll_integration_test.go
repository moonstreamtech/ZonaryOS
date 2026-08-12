// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package payroll_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/hr"
	"github.com/moonstreamtech/ZonaryOS/internal/payroll"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise payroll against a real Postgres instance - RLS
// isolation and permission enforcement can't be meaningfully faked, same
// convention as every other package's integration tests in this
// codebase. Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL and
// ZONARYOS_TEST_APP_DATABASE_URL are set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run payroll integration tests")
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
			people, contracts, payroll_periods, payroll_entries, accounts, journal_entries, journal_lines,
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

func seedMember(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmID uuid.UUID, keycloakSubject string) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test Member").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO roles (firm_id, key, name) VALUES ($1, 'member', 'Member') RETURNING id`, firmID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed member role/membership: %v", err)
	}
	return userID
}

func seedPeopleAccounts(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID uuid.UUID) {
	t.Helper()
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return accounting.SeedDefaultChartOfAccountsTx(ctx, tx, firmID, accounting.SeedChartOptions{ManagesPeople: true})
	})
	if err != nil {
		t.Fatalf("seed people accounts: %v", err)
	}
}

func seedPerson(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID, ownerID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	p, err := hr.CreatePerson(ctx, appPool, firmID, ownerID, hr.CreatePersonInput{FullName: name, Type: hr.PersonTypeEmployee})
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}
	return p.ID
}

// TestClosePeriod_PostsCorrectJournalEntriesAndRejectsDoubleClose is
// Part 1's own required test case: closing a period posts DR Salary
// Expense / CR Salaries Payable for the sum of gross_amount across the
// period's entries, and a second close is rejected without posting a
// second journal entry.
func TestClosePeriod_PostsCorrectJournalEntriesAndRejectsDoubleClose(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm Payroll", "payroll-owner")
	seedPeopleAccounts(ctx, t, appPool, firmID)
	person1 := seedPerson(ctx, t, appPool, firmID, ownerID, "Ada Lovelace")
	person2 := seedPerson(ctx, t, appPool, firmID, ownerID, "Alan Turing")

	period, err := payroll.CreatePeriod(ctx, appPool, firmID, ownerID, payroll.CreatePeriodInput{
		Name: "August 2026", PeriodStart: "2026-08-01", PeriodEnd: "2026-08-31",
	})
	if err != nil {
		t.Fatalf("CreatePeriod: %v", err)
	}

	if _, err := payroll.CreateEntry(ctx, appPool, firmID, ownerID, period.ID, payroll.EntryInput{
		PersonID: person1, GrossAmount: "10000.00", NetAmount: "9000.00",
	}); err != nil {
		t.Fatalf("CreateEntry (person1): %v", err)
	}
	if _, err := payroll.CreateEntry(ctx, appPool, firmID, ownerID, period.ID, payroll.EntryInput{
		PersonID: person2, GrossAmount: "5000.50", NetAmount: "4500.00",
	}); err != nil {
		t.Fatalf("CreateEntry (person2): %v", err)
	}

	closed, err := payroll.ClosePeriod(ctx, appPool, firmID, ownerID, period.ID)
	if err != nil {
		t.Fatalf("ClosePeriod: %v", err)
	}
	if closed.Status != payroll.PeriodStatusClosed {
		t.Fatalf("expected status closed, got %q", closed.Status)
	}

	const wantTotal = "15000.5000"
	var entryCount int
	if err := adminPool.QueryRow(ctx, `SELECT COUNT(*) FROM journal_entries WHERE source_type = 'payroll_period' AND source_id = $1`, period.ID).Scan(&entryCount); err != nil {
		t.Fatalf("count journal_entries: %v", err)
	}
	if entryCount != 1 {
		t.Fatalf("expected exactly 1 journal entry for this period after close, got %d", entryCount)
	}

	rows, err := adminPool.Query(ctx, `
		SELECT a.code, jl.side, jl.amount::text
		FROM journal_lines jl
		JOIN journal_entries je ON je.id = jl.entry_id
		JOIN accounts a ON a.id = jl.account_id
		WHERE je.source_type = 'payroll_period' AND je.source_id = $1
		ORDER BY jl.side
	`, period.ID)
	if err != nil {
		t.Fatalf("query journal_lines: %v", err)
	}
	defer rows.Close()
	var linesByCode = map[string]struct {
		side   string
		amount string
	}{}
	for rows.Next() {
		var code, side, amount string
		if err := rows.Scan(&code, &side, &amount); err != nil {
			t.Fatalf("scan journal line: %v", err)
		}
		linesByCode[code] = struct {
			side   string
			amount string
		}{side, amount}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate journal lines: %v", err)
	}

	debit, ok := linesByCode[accounting.SalaryExpenseAccountCode]
	if !ok || debit.side != "debit" || debit.amount != wantTotal {
		t.Fatalf("expected DR %s %s, got %+v", accounting.SalaryExpenseAccountCode, wantTotal, linesByCode)
	}
	credit, ok := linesByCode[accounting.SalariesPayableAccountCode]
	if !ok || credit.side != "credit" || credit.amount != wantTotal {
		t.Fatalf("expected CR %s %s, got %+v", accounting.SalariesPayableAccountCode, wantTotal, linesByCode)
	}

	// Double-close must be rejected, and must NOT post a second journal
	// entry.
	if _, err := payroll.ClosePeriod(ctx, appPool, firmID, ownerID, period.ID); !errors.Is(err, payroll.ErrPeriodAlreadyClosed) {
		t.Fatalf("expected ErrPeriodAlreadyClosed on double-close, got %v", err)
	}
	if err := adminPool.QueryRow(ctx, `SELECT COUNT(*) FROM journal_entries WHERE source_type = 'payroll_period' AND source_id = $1`, period.ID).Scan(&entryCount); err != nil {
		t.Fatalf("count journal_entries after double-close attempt: %v", err)
	}
	if entryCount != 1 {
		t.Fatalf("expected still exactly 1 journal entry after a rejected double-close, got %d", entryCount)
	}
}

func TestCreateEntry_RejectedOnceClosedAndByNonOwner(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm Payroll B", "payroll-owner-2")
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "payroll-member-2")
	seedPeopleAccounts(ctx, t, appPool, firmID)
	person := seedPerson(ctx, t, appPool, firmID, ownerID, "Grace Hopper")

	period, err := payroll.CreatePeriod(ctx, appPool, firmID, ownerID, payroll.CreatePeriodInput{
		Name: "Sept 2026", PeriodStart: "2026-09-01", PeriodEnd: "2026-09-30",
	})
	if err != nil {
		t.Fatalf("CreatePeriod: %v", err)
	}

	if _, err := payroll.CreateEntry(ctx, appPool, firmID, memberID, period.ID, payroll.EntryInput{
		PersonID: person, GrossAmount: "1000.00", NetAmount: "900.00",
	}); !errors.Is(err, payroll.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a non-owner CreateEntry, got %v", err)
	}

	if _, err := payroll.ClosePeriod(ctx, appPool, firmID, ownerID, period.ID); err != nil {
		t.Fatalf("ClosePeriod: %v", err)
	}

	if _, err := payroll.CreateEntry(ctx, appPool, firmID, ownerID, period.ID, payroll.EntryInput{
		PersonID: person, GrossAmount: "1000.00", NetAmount: "900.00",
	}); !errors.Is(err, payroll.ErrPeriodNotOpen) {
		t.Fatalf("expected ErrPeriodNotOpen once the period is closed, got %v", err)
	}
}
