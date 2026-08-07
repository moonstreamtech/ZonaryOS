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

// seedPurchaseOrderLedgerAccounts inserts the two accounts
// purchase_order.go's "receive" JournalTemplate posts against - same
// shortcut as this package's seedLedgerAccounts (stock_to_sale's own),
// but through Inventory/Trade Payables instead.
func seedPurchaseOrderLedgerAccounts(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID) {
	t.Helper()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO accounts (firm_id, code, name, type) VALUES
			($1, $2, 'Inventory', 'asset'),
			($1, $3, 'Trade Payables', 'liability')
	`, firmID, accounting.InventoryAccountCode, accounting.TradePayablesAccountCode); err != nil {
		t.Fatalf("seed purchase order ledger accounts: %v", err)
	}
}

// TestExecuteTransition_PurchaseOrderReceivePostsJournalEntry confirms
// "receive" (not "send" - see PurchaseOrderSpec's own doc comment for
// why) posts a balanced DR Inventory / CR Trade Payables entry for
// total_amount, once total_amount/supplier_name are actually supplied.
func TestExecuteTransition_PurchaseOrderReceivePostsJournalEntry(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm PO') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	seedPurchaseOrderLedgerAccounts(ctx, t, adminPool, firmID)

	definitionID, err := workflow.SeedPurchaseOrderWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedPurchaseOrderWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "po-journal-user",
		workflow.CreatePurchaseOrderPermission, workflow.SendPurchaseOrderPermission, workflow.ReceivePurchaseOrderPermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"supplier_name": "Acme Supply Co", "total_amount": float64(349.5),
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "send", nil); err != nil {
		t.Fatalf("ExecuteTransition send: %v", err)
	}

	// "send" must NOT have posted anything - see PurchaseOrderSpec's own
	// doc comment on why only "receive" carries a JournalTemplate.
	afterSend, err := accounting.ListJournalEntries(ctx, appPool, firmID, userID, accounting.ListOptions{})
	if err != nil {
		t.Fatalf("ListJournalEntries (after send): %v", err)
	}
	if afterSend.Total != 0 {
		t.Fatalf("expected 'send' to post nothing, got %d journal entries: %+v", afterSend.Total, afterSend.Entries)
	}

	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "receive", nil); err != nil {
		t.Fatalf("ExecuteTransition receive: %v", err)
	}

	result, err := accounting.ListJournalEntries(ctx, appPool, firmID, userID, accounting.ListOptions{})
	if err != nil {
		t.Fatalf("ListJournalEntries (after receive): %v", err)
	}
	if result.Total != 1 || len(result.Entries) != 1 {
		t.Fatalf("expected exactly one posted journal entry after receive, got %+v", result)
	}

	entry := result.Entries[0]
	if entry.Description != "Received purchase order from Acme Supply Co" {
		t.Errorf("expected the description to be interpolated, got %q", entry.Description)
	}
	if len(entry.Lines) != 2 {
		t.Fatalf("expected exactly two lines, got %d", len(entry.Lines))
	}
	const wantAmount = "349.5000"
	for _, l := range entry.Lines {
		if l.Amount != wantAmount {
			t.Errorf("expected line amount %q, got %q (account %q, side %q)", wantAmount, l.Amount, l.AccountCode, l.Side)
		}
		switch l.AccountCode {
		case accounting.InventoryAccountCode:
			if l.Side != accounting.SideDebit {
				t.Errorf("expected Inventory to be debited, got side %q", l.Side)
			}
		case accounting.TradePayablesAccountCode:
			if l.Side != accounting.SideCredit {
				t.Errorf("expected Trade Payables to be credited, got side %q", l.Side)
			}
		default:
			t.Errorf("unexpected account code %q on posted entry", l.AccountCode)
		}
	}
}

// TestSeedDefaultChartOfAccounts_InventorySeededForPurchasesWithoutSales
// confirms internal/accounting.SeedDefaultChartOfAccountsTx seeds
// InventoryAccountCode even for a firm that purchases from suppliers but
// does NOT sell products - the exact gap that would otherwise make
// "receive" fail with ErrAccountNotFound (see seed.go's own doc comment
// on purchasesAccounts for the full reasoning).
func TestSeedDefaultChartOfAccounts_InventorySeededForPurchasesWithoutSales(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm PO Only') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	err := func() error {
		tx, err := appPool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_firm_id', $1, true)", firmID.String()); err != nil {
			return err
		}
		if err := accounting.SeedDefaultChartOfAccountsTx(ctx, tx, firmID, accounting.SeedChartOptions{
			Sells: false, PurchasesFromSuppliers: true,
		}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}()
	if err != nil {
		t.Fatalf("seed chart of accounts: %v", err)
	}

	var inventoryCount int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE firm_id = $1 AND code = $2`, firmID, accounting.InventoryAccountCode).Scan(&inventoryCount); err != nil {
		t.Fatalf("count inventory accounts: %v", err)
	}
	if inventoryCount != 1 {
		t.Fatalf("expected exactly one Inventory account to be seeded for a purchases-only firm, got %d", inventoryCount)
	}
}
