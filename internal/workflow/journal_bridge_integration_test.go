// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// seedLedgerAccounts inserts the two accounts stock_to_sale.go's
// record_sale JournalTemplate posts against, directly via adminPool (the
// migrator's own privileged role, which - like every table owner -
// bypasses RLS, the same shortcut this file's sibling tests already take
// for seeding firms/users) rather than through the owner-gated
// accounting.CreateAccount, to keep this test focused on the bridge
// itself rather than account provisioning.
func seedLedgerAccounts(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID) {
	t.Helper()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO accounts (firm_id, code, name, type) VALUES
			($1, $2, 'Trade Receivables', 'asset'),
			($1, $3, 'Sales Revenue', 'revenue')
	`, firmID, accounting.TradeReceivablesAccountCode, accounting.SalesRevenueAccountCode); err != nil {
		t.Fatalf("seed ledger accounts: %v", err)
	}
}

// TestExecuteTransition_RecordSalePostsJournalEntry is this batch's real
// working example (design brief): recording a sale, with both quantity
// and unit_price present in the payload, posts a balanced DR Trade
// Receivables / CR Sales Revenue journal entry for quantity * unit_price.
func TestExecuteTransition_RecordSalePostsJournalEntry(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	seedLedgerAccounts(ctx, t, adminPool, firmID)

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sale-user", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget", "quantity": float64(3)})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", map[string]any{"unit_price": float64(19.99)}); err != nil {
		t.Fatalf("ExecuteTransition record_sale: %v", err)
	}

	result, err := accounting.ListJournalEntries(ctx, appPool, firmID, userID, accounting.ListOptions{})
	if err != nil {
		t.Fatalf("ListJournalEntries: %v", err)
	}
	if result.Total != 1 || len(result.Entries) != 1 {
		t.Fatalf("expected exactly one posted journal entry, got %+v", result)
	}

	entry := result.Entries[0]
	if entry.SourceType != "workflow_instance" || entry.SourceID == nil || *entry.SourceID != instanceID {
		t.Errorf("expected the entry to trace back to the workflow instance, got sourceType=%q sourceId=%v", entry.SourceType, entry.SourceID)
	}
	if entry.Description != "Sale of widget" {
		t.Errorf("expected the description to be interpolated to %q, got %q", "Sale of widget", entry.Description)
	}
	if len(entry.Lines) != 2 {
		t.Fatalf("expected exactly two lines, got %d", len(entry.Lines))
	}
	// 3 * 19.99 = 59.97
	const wantAmount = "59.9700"
	for _, l := range entry.Lines {
		if l.Amount != wantAmount {
			t.Errorf("expected line amount %q, got %q (account %q, side %q)", wantAmount, l.Amount, l.AccountCode, l.Side)
		}
		switch l.AccountCode {
		case accounting.TradeReceivablesAccountCode:
			if l.Side != accounting.SideDebit {
				t.Errorf("expected trade receivables to be debited, got side %q", l.Side)
			}
		case accounting.SalesRevenueAccountCode:
			if l.Side != accounting.SideCredit {
				t.Errorf("expected sales revenue to be credited, got side %q", l.Side)
			}
		default:
			t.Errorf("unexpected account code %q on posted entry", l.AccountCode)
		}
	}
}

// TestExecuteTransition_RecordSaleWithoutPriceFieldsSkipsPosting is the
// Rule 6 regression test: record_sale executed with no quantity/unit_price
// in the payload (every ExecuteTransition("record_sale", ...) call site
// that predates this batch, e.g. this package's own pre-existing
// integration tests) must keep succeeding exactly as before, and must not
// post a journal entry at all - see resolveJournalLines' doc comment
// (engine.go) for why a missing amount field is a skip, not a failure.
func TestExecuteTransition_RecordSaleWithoutPriceFieldsSkipsPosting(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	// Deliberately NOT seeding the ledger accounts here: if this bridge
	// somehow tried to post anyway, PostJournalEntryTx would fail on an
	// unknown account code, immediately surfacing the bug this test
	// exists to catch, rather than a silent false pass.

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sale-user-2", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", nil); err != nil {
		t.Fatalf("ExecuteTransition record_sale (no price fields): %v", err)
	}

	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.State.Key != "sold" {
		t.Errorf("expected the instance to still transition to 'sold', got %q", state.State.Key)
	}
}
