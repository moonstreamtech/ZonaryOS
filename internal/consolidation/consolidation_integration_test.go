// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package consolidation_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/consolidation"
	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
)

// These tests exercise internal/consolidation against a real Postgres
// instance - RLS isolation (including the atomic two-firm transaction
// this package's own PostIntercompanyTransfer establishes for the first
// time in this codebase) can't be meaningfully faked, same convention
// every other package's integration tests in this codebase follow.
// Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL and
// ZONARYOS_TEST_APP_DATABASE_URL are set.

const adminEmail = "admin@zonaryos.example"

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run consolidation integration tests")
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
			accounts, journal_entries, journal_lines,
			firm_groups, firm_group_members, intercompany_transactions,
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

func adminCaller() identity.Identity {
	return identity.Identity{Subject: "platform-admin-sub", Email: adminEmail, DisplayName: "Platform Admin"}
}

func allowAdmin() platformadmin.Allowlist {
	return platformadmin.NewAllowlist([]string{adminEmail})
}

// TestAddGroupMember_PlatformAdminOnly confirms a caller off the
// allowlist (a firm owner, in this test) cannot create a group or modify
// membership - the design brief's "individual firm owners ... cannot
// modify group membership."
func TestAddGroupMember_PlatformAdminOnly(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "group-owner")
	_ = ownerID
	notAdmin := identity.Identity{Subject: "firm-owner-sub", Email: "owner@example.com", DisplayName: "Firm Owner"}
	allow := allowAdmin()

	if _, err := consolidation.CreateFirmGroup(ctx, appPool, allow, notAdmin, "Holding Co"); !errors.Is(err, platformadmin.ErrNotPlatformAdmin) {
		t.Fatalf("expected CreateFirmGroup by a non-admin to fail with ErrNotPlatformAdmin, got %v", err)
	}

	group, err := consolidation.CreateFirmGroup(ctx, appPool, allow, adminCaller(), "Holding Co")
	if err != nil {
		t.Fatalf("CreateFirmGroup (admin): %v", err)
	}
	if _, err := consolidation.AddGroupMember(ctx, appPool, allow, notAdmin, group.ID, firmID, consolidation.FirmGroupRoleSubsidiary, nil); !errors.Is(err, platformadmin.ErrNotPlatformAdmin) {
		t.Fatalf("expected AddGroupMember by a non-admin to fail with ErrNotPlatformAdmin, got %v", err)
	}
}

// TestGetFirmGroupForFirm_MemberCanViewNoFinancialData confirms a firm
// owner can see which group their firm belongs to and their fellow
// members' names/roles, and confirms the shape carries no financial
// data at all (Task's own explicit boundary) - and that a firm not in
// any group gets (nil, nil), not an error.
func TestGetFirmGroupForFirm_MemberCanViewNoFinancialData(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	parentID, parentOwnerID := seedOwner(ctx, t, adminPool, appPool, "Parent Co", "group-parent-owner")
	subID, _ := seedOwner(ctx, t, adminPool, appPool, "Sub Co", "group-sub-owner")
	loneID, loneOwnerID := seedOwner(ctx, t, adminPool, appPool, "Lone Co", "group-lone-owner")

	allow := allowAdmin()
	group, err := consolidation.CreateFirmGroup(ctx, appPool, allow, adminCaller(), "Holding Co")
	if err != nil {
		t.Fatalf("CreateFirmGroup: %v", err)
	}
	pct := "80.00"
	if _, err := consolidation.AddGroupMember(ctx, appPool, allow, adminCaller(), group.ID, parentID, consolidation.FirmGroupRoleParent, nil); err != nil {
		t.Fatalf("AddGroupMember (parent): %v", err)
	}
	if _, err := consolidation.AddGroupMember(ctx, appPool, allow, adminCaller(), group.ID, subID, consolidation.FirmGroupRoleSubsidiary, &pct); err != nil {
		t.Fatalf("AddGroupMember (sub): %v", err)
	}

	detail, err := consolidation.GetFirmGroupForFirm(ctx, appPool, parentID, parentOwnerID)
	if err != nil {
		t.Fatalf("GetFirmGroupForFirm: %v", err)
	}
	if detail == nil {
		t.Fatal("expected parentID to be in a group")
	}
	if detail.Name != "Holding Co" {
		t.Errorf("expected group name %q, got %q", "Holding Co", detail.Name)
	}
	if len(detail.Members) != 2 {
		t.Fatalf("expected 2 members, got %+v", detail.Members)
	}
	byFirm := make(map[uuid.UUID]consolidation.FirmGroupMemberView, len(detail.Members))
	for _, m := range detail.Members {
		byFirm[m.FirmID] = m
	}
	if byFirm[subID].OwnershipPct == nil || *byFirm[subID].OwnershipPct != "80.00" {
		t.Errorf("expected sub's ownership_pct 80.00, got %v", byFirm[subID].OwnershipPct)
	}
	if byFirm[subID].FirmName != "Sub Co" {
		t.Errorf("expected sub's firm name %q, got %q", "Sub Co", byFirm[subID].FirmName)
	}

	loneDetail, err := consolidation.GetFirmGroupForFirm(ctx, appPool, loneID, loneOwnerID)
	if err != nil {
		t.Fatalf("GetFirmGroupForFirm (lone firm): %v", err)
	}
	if loneDetail != nil {
		t.Errorf("expected a firm in no group to get a nil detail, got %+v", loneDetail)
	}
}

func seedTransferGroup(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool) (groupID, fromFirmID, toFirmID uuid.UUID) {
	t.Helper()
	fromFirmID, _ = seedOwner(ctx, t, adminPool, appPool, "Firm A", "ic-from-owner")
	toFirmID, _ = seedOwner(ctx, t, adminPool, appPool, "Firm B", "ic-to-owner")

	allow := allowAdmin()
	group, err := consolidation.CreateFirmGroup(ctx, appPool, allow, adminCaller(), "Group AB")
	if err != nil {
		t.Fatalf("CreateFirmGroup: %v", err)
	}
	if _, err := consolidation.AddGroupMember(ctx, appPool, allow, adminCaller(), group.ID, fromFirmID, consolidation.FirmGroupRoleParent, nil); err != nil {
		t.Fatalf("AddGroupMember (from): %v", err)
	}
	if _, err := consolidation.AddGroupMember(ctx, appPool, allow, adminCaller(), group.ID, toFirmID, consolidation.FirmGroupRoleSubsidiary, nil); err != nil {
		t.Fatalf("AddGroupMember (to): %v", err)
	}
	return group.ID, fromFirmID, toFirmID
}

// TestPostIntercompanyTransfer_PostsBothJournalEntriesAtomically confirms
// a transfer posts a real, balanced journal entry into EACH firm's own
// books, ties them together via intercompany_transactions, and that a
// firm outside the group is rejected.
func TestPostIntercompanyTransfer_PostsBothJournalEntriesAtomically(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	groupID, fromFirmID, toFirmID := seedTransferGroup(ctx, t, adminPool, appPool)
	allow := allowAdmin()

	outsiderID, _ := seedOwner(ctx, t, adminPool, appPool, "Outsider Co", "ic-outsider-owner")
	if _, err := consolidation.PostIntercompanyTransfer(ctx, appPool, allow, adminCaller(), groupID, fromFirmID, outsiderID, "100.00", "TRY", "should fail"); !errors.Is(err, consolidation.ErrFirmNotInGroup) {
		t.Fatalf("expected a transfer to a firm outside the group to fail with ErrFirmNotInGroup, got %v", err)
	}

	txn, err := consolidation.PostIntercompanyTransfer(ctx, appPool, allow, adminCaller(), groupID, fromFirmID, toFirmID, "500.00", "TRY", "Cash advance")
	if err != nil {
		t.Fatalf("PostIntercompanyTransfer: %v", err)
	}
	if txn.FromJournalEntryID == nil || txn.ToJournalEntryID == nil {
		t.Fatalf("expected both journal entry ids to be set, got %+v", txn)
	}

	var fromEntryCount, toEntryCount int
	if err := zdb.WithFirmContext(ctx, appPool, fromFirmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE id = $1`, *txn.FromJournalEntryID).Scan(&fromEntryCount)
	}); err != nil {
		t.Fatalf("check from-firm journal entry: %v", err)
	}
	if fromEntryCount != 1 {
		t.Errorf("expected exactly 1 journal entry in the from-firm's own books, got %d", fromEntryCount)
	}
	if err := zdb.WithFirmContext(ctx, appPool, toFirmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE id = $1`, *txn.ToJournalEntryID).Scan(&toEntryCount)
	}); err != nil {
		t.Fatalf("check to-firm journal entry: %v", err)
	}
	if toEntryCount != 1 {
		t.Errorf("expected exactly 1 journal entry in the to-firm's own books, got %d", toEntryCount)
	}

	var storedCount int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM intercompany_transactions WHERE id = $1`, txn.ID).Scan(&storedCount); err != nil {
		t.Fatalf("check intercompany_transactions row: %v", err)
	}
	if storedCount != 1 {
		t.Errorf("expected exactly 1 intercompany_transactions row, got %d", storedCount)
	}
}

// TestPostIntercompanyTransfer_FailureRollsBackBothFirms confirms that
// when posting the SECOND firm's journal entry fails, the FIRST firm's
// entry (already inserted on the same, still-open transaction) is rolled
// back too - the entire point of zdb.WithTwoFirmContext over two
// independent WithFirmContext calls. Forces the failure by pre-creating
// the to-firm's Cash account as inactive, which
// accounting.PostJournalEntryTx rejects with ErrAccountInactive.
func TestPostIntercompanyTransfer_FailureRollsBackBothFirms(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	groupID, fromFirmID, toFirmID := seedTransferGroup(ctx, t, adminPool, appPool)

	if _, err := accounting.CreateAccount(ctx, appPool, toFirmID, mustGetOwnerID(ctx, t, adminPool, toFirmID), "1000", "Cash", accounting.AccountTypeAsset, nil, ""); err != nil {
		t.Fatalf("seed inactive-to-be cash account: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `UPDATE accounts SET is_active = false WHERE firm_id = $1 AND code = '1000'`, toFirmID); err != nil {
		t.Fatalf("deactivate to-firm cash account: %v", err)
	}

	allow := allowAdmin()
	_, err := consolidation.PostIntercompanyTransfer(ctx, appPool, allow, adminCaller(), groupID, fromFirmID, toFirmID, "250.00", "TRY", "should roll back")
	if err == nil {
		t.Fatal("expected PostIntercompanyTransfer to fail when the to-firm's cash account is inactive")
	}

	var fromEntryCount int
	if err := zdb.WithFirmContext(ctx, appPool, fromFirmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM journal_entries`).Scan(&fromEntryCount)
	}); err != nil {
		t.Fatalf("count from-firm journal entries: %v", err)
	}
	if fromEntryCount != 0 {
		t.Errorf("expected the from-firm's own journal entry to be rolled back too (atomicity across both firms), got %d entries", fromEntryCount)
	}

	var storedCount int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM intercompany_transactions`).Scan(&storedCount); err != nil {
		t.Fatalf("count intercompany_transactions: %v", err)
	}
	if storedCount != 0 {
		t.Errorf("expected no intercompany_transactions row on failure, got %d", storedCount)
	}
}

func mustGetOwnerID(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		SELECT u.id FROM users u
		JOIN user_firm_roles ufr ON ufr.user_id = u.id
		WHERE ufr.firm_id = $1 LIMIT 1
	`, firmID).Scan(&userID); err != nil {
		t.Fatalf("look up firm owner: %v", err)
	}
	return userID
}

// TestGetConsolidatedPnL_EliminatesIntercompanyAndSumsAccurately seeds
// ordinary (non-intercompany) revenue in two member firms, then a THIRD
// journal entry manually tagged as an intercompany leg (an
// intercompany_transactions row pointing at it, the same shape
// PostIntercompanyTransfer itself creates) - and confirms the
// consolidated P&L includes the two ordinary entries but excludes the
// tagged one, proving the elimination is a real filter against
// intercompany_transactions, not just an artifact of which account types
// PostIntercompanyTransfer happens to touch.
func TestGetConsolidatedPnL_EliminatesIntercompanyAndSumsAccurately(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmAID, ownerAID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "pnl-a-owner")
	firmBID, ownerBID := seedOwner(ctx, t, adminPool, appPool, "Firm B", "pnl-b-owner")

	allow := allowAdmin()
	group, err := consolidation.CreateFirmGroup(ctx, appPool, allow, adminCaller(), "Group PnL")
	if err != nil {
		t.Fatalf("CreateFirmGroup: %v", err)
	}
	if _, err := consolidation.AddGroupMember(ctx, appPool, allow, adminCaller(), group.ID, firmAID, consolidation.FirmGroupRoleParent, nil); err != nil {
		t.Fatalf("AddGroupMember (A): %v", err)
	}
	if _, err := consolidation.AddGroupMember(ctx, appPool, allow, adminCaller(), group.ID, firmBID, consolidation.FirmGroupRoleSubsidiary, nil); err != nil {
		t.Fatalf("AddGroupMember (B): %v", err)
	}

	// Ordinary revenue in each firm, unrelated to any intercompany
	// transfer.
	if _, err := accounting.CreateAccount(ctx, appPool, firmAID, ownerAID, "1100", "Trade Receivables", accounting.AccountTypeAsset, nil, ""); err != nil {
		t.Fatalf("seed A receivables: %v", err)
	}
	if _, err := accounting.CreateAccount(ctx, appPool, firmAID, ownerAID, "4000", "Sales Revenue", accounting.AccountTypeRevenue, nil, ""); err != nil {
		t.Fatalf("seed A revenue account: %v", err)
	}
	if _, err := accounting.PostJournalEntry(ctx, appPool, firmAID, ownerAID, "Ordinary sale A", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "1000.00"},
		{AccountCode: "4000", Side: accounting.SideCredit, Amount: "1000.00"},
	}); err != nil {
		t.Fatalf("post ordinary sale A: %v", err)
	}

	if _, err := accounting.CreateAccount(ctx, appPool, firmBID, ownerBID, "1100", "Trade Receivables", accounting.AccountTypeAsset, nil, ""); err != nil {
		t.Fatalf("seed B receivables: %v", err)
	}
	if _, err := accounting.CreateAccount(ctx, appPool, firmBID, ownerBID, "4000", "Sales Revenue", accounting.AccountTypeRevenue, nil, ""); err != nil {
		t.Fatalf("seed B revenue account: %v", err)
	}
	if _, err := accounting.PostJournalEntry(ctx, appPool, firmBID, ownerBID, "Ordinary sale B", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "400.00"},
		{AccountCode: "4000", Side: accounting.SideCredit, Amount: "400.00"},
	}); err != nil {
		t.Fatalf("post ordinary sale B: %v", err)
	}

	// A THIRD entry in firm A that touches Sales Revenue - manually
	// tagged as an intercompany leg (simulating an intercompany SALE, not
	// just PostIntercompanyTransfer's own cash-advance shape) so the
	// elimination filter has something real to exclude.
	intercompanyEntryID, err := accounting.PostJournalEntry(ctx, appPool, firmAID, ownerAID, "Intercompany sale to B", []accounting.LineInput{
		{AccountCode: "1100", Side: accounting.SideDebit, Amount: "300.00"},
		{AccountCode: "4000", Side: accounting.SideCredit, Amount: "300.00"},
	})
	if err != nil {
		t.Fatalf("post intercompany-tagged entry: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO intercompany_transactions (group_id, from_firm_id, to_firm_id, amount, currency, description, from_journal_entry_id)
		VALUES ($1, $2, $3, 300.00, 'TRY', 'Intercompany sale to B', $4)
	`, group.ID, firmAID, firmBID, intercompanyEntryID); err != nil {
		t.Fatalf("tag entry as intercompany: %v", err)
	}

	report, err := consolidation.GetConsolidatedPnL(ctx, appPool, allow, adminCaller(), group.ID, nil, nil)
	if err != nil {
		t.Fatalf("GetConsolidatedPnL: %v", err)
	}
	if report.TotalRevenue != "1400.0000" {
		t.Errorf("expected total revenue 1400.0000 (1000 A + 400 B, excluding the 300 intercompany-tagged entry), got %q", report.TotalRevenue)
	}
	if len(report.Firms) != 2 {
		t.Fatalf("expected 2 firm lines, got %+v", report.Firms)
	}
	byFirm := make(map[uuid.UUID]consolidation.ConsolidatedPnLFirmLine, len(report.Firms))
	for _, f := range report.Firms {
		byFirm[f.FirmID] = f
	}
	if byFirm[firmAID].Revenue != "1000.0000" {
		t.Errorf("expected firm A's own revenue 1000.0000 (excluding its 300 intercompany-tagged entry), got %q", byFirm[firmAID].Revenue)
	}
	if byFirm[firmBID].Revenue != "400.0000" {
		t.Errorf("expected firm B's own revenue 400.0000, got %q", byFirm[firmBID].Revenue)
	}
}
