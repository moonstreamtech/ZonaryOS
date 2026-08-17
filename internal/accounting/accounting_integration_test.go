// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package accounting_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/queryfilter"
)

// These tests exercise the financial management core against a real
// Postgres instance - RLS isolation and the balance invariant can't be
// meaningfully faked, same convention as every other package's
// integration tests in this codebase. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run accounting integration tests")
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
			accounts, journal_entries, journal_lines, budgets, budget_lines,
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

// seedOwner creates a firm with an owner-flagged role and a member holding
// it - mirrors internal/wizard.CreateDefaultFirm's own shape closely
// enough for these tests (owner-gated CreateAccount/PostJournalEntry)
// without going through the wizard package itself (would be a
// wizard->accounting->wizard-ish layering smell for a test fixture).
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

func seedAccount(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID, userID uuid.UUID, code, name string, typ accounting.AccountType) {
	t.Helper()
	if _, err := accounting.CreateAccount(ctx, appPool, firmID, userID, code, name, typ, nil, ""); err != nil {
		t.Fatalf("seed account %q: %v", code, err)
	}
}

func TestPostJournalEntry_RejectsUnbalancedEntry(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "owner-unbalanced")
	seedAccount(ctx, t, appPool, firmID, userID, "1100", "Trade Receivables", accounting.AccountTypeAsset)
	seedAccount(ctx, t, appPool, firmID, userID, "4000", "Sales Revenue", accounting.AccountTypeRevenue)

	_, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "Unbalanced sale", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "100.00"},
		{AccountCode: "4000", Side: accounting.SideCredit, Amount: "90.00"},
	})
	if !errors.Is(err, accounting.ErrUnbalancedEntry) {
		t.Fatalf("expected ErrUnbalancedEntry, got %v", err)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count journal entries: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected the unbalanced entry to leave no row behind (rolled back), got %d journal_entries rows", count)
	}
}

func TestPostJournalEntry_BalancedEntryPostsAndUpdatesBalances(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "owner-balanced")
	seedAccount(ctx, t, appPool, firmID, userID, "1100", "Trade Receivables", accounting.AccountTypeAsset)
	seedAccount(ctx, t, appPool, firmID, userID, "4000", "Sales Revenue", accounting.AccountTypeRevenue)

	entryID, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "Sale of widget", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "150.00"},
		{AccountCode: "4000", Side: accounting.SideCredit, Amount: "150.00"},
	})
	if err != nil {
		t.Fatalf("PostJournalEntry: %v", err)
	}
	if entryID == uuid.Nil {
		t.Fatal("expected a non-nil entry id")
	}

	var receivablesID, revenueID uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT id FROM accounts WHERE firm_id = $1 AND code = '1100'`, firmID).Scan(&receivablesID); err != nil {
		t.Fatalf("look up receivables account: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `SELECT id FROM accounts WHERE firm_id = $1 AND code = '4000'`, firmID).Scan(&revenueID); err != nil {
		t.Fatalf("look up revenue account: %v", err)
	}

	receivablesBalance, err := accounting.GetAccountBalance(ctx, appPool, firmID, userID, receivablesID)
	if err != nil {
		t.Fatalf("GetAccountBalance (receivables): %v", err)
	}
	if receivablesBalance != "150.0000" {
		t.Errorf("expected receivables balance 150.0000 (asset, normal debit), got %q", receivablesBalance)
	}

	revenueBalance, err := accounting.GetAccountBalance(ctx, appPool, firmID, userID, revenueID)
	if err != nil {
		t.Fatalf("GetAccountBalance (revenue): %v", err)
	}
	if revenueBalance != "150.0000" {
		t.Errorf("expected revenue balance 150.0000 (revenue, normal credit), got %q", revenueBalance)
	}

	result, err := accounting.ListJournalEntries(ctx, appPool, firmID, userID, accounting.ListOptions{})
	if err != nil {
		t.Fatalf("ListJournalEntries: %v", err)
	}
	if result.Total != 1 || len(result.Entries) != 1 {
		t.Fatalf("expected exactly one journal entry, got %+v", result)
	}
	if len(result.Entries[0].Lines) != 2 {
		t.Fatalf("expected exactly two lines, got %d", len(result.Entries[0].Lines))
	}
}

func TestPostJournalEntry_NonOwnerDenied(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "owner-denied")
	seedAccount(ctx, t, appPool, firmID, ownerID, "1100", "Trade Receivables", accounting.AccountTypeAsset)
	seedAccount(ctx, t, appPool, firmID, ownerID, "4000", "Sales Revenue", accounting.AccountTypeRevenue)

	var memberID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ('member-denied', 'member-denied@example.com', 'Member') RETURNING id
	`).Scan(&memberID); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO roles (firm_id, key, name) VALUES ($1, 'member', 'Member') RETURNING id`, firmID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, memberID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed non-owner member: %v", err)
	}

	_, err = accounting.PostJournalEntry(ctx, appPool, firmID, memberID, "Should be denied", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "10.00"},
		{AccountCode: "4000", Side: accounting.SideCredit, Amount: "10.00"},
	})
	if !errors.Is(err, accounting.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner, got %v", err)
	}

	if _, err := accounting.CreateAccount(ctx, appPool, firmID, memberID, "9999", "Should be denied", accounting.AccountTypeAsset, nil, ""); !errors.Is(err, accounting.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner from CreateAccount, got %v", err)
	}
}

func TestAccounts_CrossFirmIsolation(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmA, ownerA := seedOwner(ctx, t, adminPool, appPool, "Firm A", "owner-a")
	firmB, ownerB := seedOwner(ctx, t, adminPool, appPool, "Firm B", "owner-b")

	seedAccount(ctx, t, appPool, firmA, ownerA, "1100", "Firm A Receivables", accounting.AccountTypeAsset)
	seedAccount(ctx, t, appPool, firmB, ownerB, "1100", "Firm B Receivables", accounting.AccountTypeAsset)

	accountsA, err := accounting.ListAccounts(ctx, appPool, firmA, ownerA)
	if err != nil {
		t.Fatalf("ListAccounts (firm A): %v", err)
	}
	if len(accountsA) != 1 || accountsA[0].Name != "Firm A Receivables" {
		t.Fatalf("expected firm A to see only its own account, got %+v", accountsA)
	}

	// Firm A's owner must not be able to see firm B's ledger at all -
	// ownerB is not a member of firmA, so this must fail closed
	// (ErrFirmNotFound), not merely return an empty/filtered list -
	// mirrors internal/workflow's own cross-firm isolation tests.
	if _, err := accounting.ListAccounts(ctx, appPool, firmA, ownerB); !errors.Is(err, accounting.ErrFirmNotFound) {
		t.Fatalf("expected ErrFirmNotFound when a firm B owner reads firm A's accounts, got %v", err)
	}

	entryID, err := accounting.PostJournalEntry(ctx, appPool, firmA, ownerA, "Firm A only", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "20.00"},
		{AccountCode: "1100", Side: accounting.SideCredit, Amount: "20.00"},
	})
	if err != nil {
		t.Fatalf("PostJournalEntry (firm A): %v", err)
	}
	_ = entryID

	resultB, err := accounting.ListJournalEntries(ctx, appPool, firmB, ownerB, accounting.ListOptions{})
	if err != nil {
		t.Fatalf("ListJournalEntries (firm B): %v", err)
	}
	if resultB.Total != 0 || len(resultB.Entries) != 0 {
		t.Fatalf("expected firm B to see no journal entries from firm A, got %+v", resultB)
	}
}

// TestListJournalEntries_DateRangeFilter is Part 2's own required test
// case: a date range filter on journal_entries.posted_at (via
// internal/queryfilter's shared `{field, op, value}` vocabulary) keeps
// only entries posted in that range.
func TestListJournalEntries_DateRangeFilter(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm Date Filter", "owner-date-filter")
	seedAccount(ctx, t, appPool, firmID, userID, "1100", "Trade Receivables", accounting.AccountTypeAsset)
	seedAccount(ctx, t, appPool, firmID, userID, "4000", "Sales Revenue", accounting.AccountTypeRevenue)

	oldEntryID, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "Old sale", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "50.00"},
		{AccountCode: "4000", Side: accounting.SideCredit, Amount: "50.00"},
	})
	if err != nil {
		t.Fatalf("PostJournalEntry (old): %v", err)
	}
	// Backdate directly - PostJournalEntry itself always uses now(), so
	// this is the only way to get a real, distinct posted_at to filter
	// against in a test.
	oldPostedAt := time.Now().AddDate(0, 0, -30)
	if _, err := adminPool.Exec(ctx, `UPDATE journal_entries SET posted_at = $1 WHERE id = $2`, oldPostedAt, oldEntryID); err != nil {
		t.Fatalf("backdate old entry: %v", err)
	}

	recentEntryID, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "Recent sale", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "75.00"},
		{AccountCode: "4000", Side: accounting.SideCredit, Amount: "75.00"},
	})
	if err != nil {
		t.Fatalf("PostJournalEntry (recent): %v", err)
	}

	from := time.Now().AddDate(0, 0, -1)
	result, err := accounting.ListJournalEntries(ctx, appPool, firmID, userID, accounting.ListOptions{
		Filters: []queryfilter.Filter{
			{Field: "posted_at", Op: queryfilter.OpGte, Value: from},
		},
	})
	if err != nil {
		t.Fatalf("ListJournalEntries (date range): %v", err)
	}
	if result.Total != 1 || len(result.Entries) != 1 || result.Entries[0].ID != recentEntryID {
		t.Fatalf("expected only the recent entry (%s) to match the date range filter, got %+v", recentEntryID, result)
	}
}
