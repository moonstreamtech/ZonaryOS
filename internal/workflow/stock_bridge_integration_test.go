// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// seedProduct inserts a products row directly via adminPool - same
// shortcut as this package's seedLedgerAccounts (bypasses the owner-gated
// inventory.CreateProduct to keep these tests focused on the bridge
// itself, not product provisioning).
func seedProduct(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID, sku string, costPrice string) uuid.UUID {
	t.Helper()
	var productID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO products (firm_id, sku, name, cost_price) VALUES ($1, $2, $2, $3) RETURNING id
	`, firmID, sku, costPrice).Scan(&productID); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	return productID
}

// seedInitialStock gives productID an initial on-hand quantity at the
// "default" location, directly via adminPool.
func seedInitialStock(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID, productID uuid.UUID, quantity string) {
	t.Helper()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO stock_levels (firm_id, product_id, location, quantity) VALUES ($1, $2, 'default', $3::numeric)
	`, firmID, productID, quantity); err != nil {
		t.Fatalf("seed initial stock: %v", err)
	}
}

// TestExecuteTransition_RecordSaleDecreasesStock is the design brief's
// core transactional-wiring example: recording a sale with product_id and
// quantity present decreases stock_levels.quantity by quantity and
// appends a stock_movements row with reason "sale" - in the same
// transaction as the journal entry and the state change themselves (see
// engine.go's ExecuteTransition, the AdjustStockTx call right after the
// journal-posting block).
func TestExecuteTransition_RecordSaleDecreasesStock(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Stock') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	// Deliberately no ledger accounts seeded: this test only cares about
	// product_id/quantity present with no unit_price, so the Journal
	// template skips (resolveJournalLines' own "missing field means skip"
	// contract) - if it somehow tried to post anyway, PostJournalEntryTx
	// would fail on an unknown account code, immediately surfacing the bug.

	productID := seedProduct(ctx, t, adminPool, firmID, "WIDGET-1", "5.00")
	seedInitialStock(ctx, t, adminPool, firmID, productID, "10")

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "stock-sale-user", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"item": "widget", "product_id": productID.String(),
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", map[string]any{"quantity": float64(3)}); err != nil {
		t.Fatalf("ExecuteTransition record_sale: %v", err)
	}

	var remaining string
	if err := adminPool.QueryRow(ctx, `SELECT quantity::text FROM stock_levels WHERE product_id = $1`, productID).Scan(&remaining); err != nil {
		t.Fatalf("read stock level: %v", err)
	}
	if remaining != "7.0000" {
		t.Errorf("expected remaining quantity 7.0000, got %q", remaining)
	}

	var movementCount int
	var movementReason, movementChange string
	var movementSourceID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM stock_movements WHERE product_id = $1
	`, productID).Scan(&movementCount); err != nil {
		t.Fatalf("count stock movements: %v", err)
	}
	if movementCount != 1 {
		t.Fatalf("expected exactly one stock movement, got %d", movementCount)
	}
	if err := adminPool.QueryRow(ctx, `
		SELECT reason, quantity_change::text, source_id FROM stock_movements WHERE product_id = $1
	`, productID).Scan(&movementReason, &movementChange, &movementSourceID); err != nil {
		t.Fatalf("read stock movement: %v", err)
	}
	if movementReason != "sale" {
		t.Errorf("expected reason %q, got %q", "sale", movementReason)
	}
	if movementChange != "-3.0000" {
		t.Errorf("expected quantity_change -3.0000, got %q", movementChange)
	}
	if movementSourceID != instanceID {
		t.Errorf("expected the movement to trace back to the workflow instance, got sourceId=%v", movementSourceID)
	}
}

// TestExecuteTransition_RecordSaleRejectsInsufficientStock is the design
// brief's explicit "reject the transition, not just warn" requirement: a
// sale that would take quantity below zero fails ExecuteTransition
// entirely (ErrInsufficientStock), and rolls back the state change with
// it - the instance must still be "in_stock", not "sold".
func TestExecuteTransition_RecordSaleRejectsInsufficientStock(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Stock 2') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	productID := seedProduct(ctx, t, adminPool, firmID, "WIDGET-2", "5.00")
	seedInitialStock(ctx, t, adminPool, firmID, productID, "2")

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "stock-sale-user-2", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"item": "widget", "product_id": productID.String(),
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	err = workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", map[string]any{"quantity": float64(5)})
	if !errors.Is(err, inventory.ErrInsufficientStock) {
		t.Fatalf("expected ErrInsufficientStock, got %v", err)
	}

	var remaining string
	if err := adminPool.QueryRow(ctx, `SELECT quantity::text FROM stock_levels WHERE product_id = $1`, productID).Scan(&remaining); err != nil {
		t.Fatalf("read stock level: %v", err)
	}
	if remaining != "2.0000" {
		t.Errorf("expected stock to be unchanged at 2.0000, got %q", remaining)
	}

	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.State.Key != "in_stock" {
		t.Errorf("expected the state change to have rolled back (still 'in_stock'), got %q", state.State.Key)
	}
}

// TestExecuteTransition_RecordSaleWithoutProductIDSkipsStockAdjustment is
// the Rule 6 regression test for the inventory bridge, the exact
// counterpart to TestExecuteTransition_RecordSaleWithoutPriceFieldsSkipsPosting
// for the journal bridge: record_sale executed with no product_id in the
// payload (every ExecuteTransition("record_sale", ...) call site that
// predates this batch) must keep succeeding, and must not touch
// stock_levels/stock_movements at all.
func TestExecuteTransition_RecordSaleWithoutProductIDSkipsStockAdjustment(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Stock 3') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "stock-sale-user-3", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", map[string]any{"quantity": float64(3)}); err != nil {
		t.Fatalf("ExecuteTransition record_sale (no product_id): %v", err)
	}

	var movementCount int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM stock_movements WHERE firm_id = $1`, firmID).Scan(&movementCount); err != nil {
		t.Fatalf("count stock movements: %v", err)
	}
	if movementCount != 0 {
		t.Errorf("expected no stock movements without product_id, got %d", movementCount)
	}
}

// TestExecuteTransition_PurchaseOrderReceiveIncreasesStock is the
// "purchase_receive" hook's own working example: receiving a purchase
// order with product_id/quantity present increases stock_levels.quantity
// and appends a stock_movements row with reason "purchase".
func TestExecuteTransition_PurchaseOrderReceiveIncreasesStock(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm PO Stock') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	productID := seedProduct(ctx, t, adminPool, firmID, "WIDGET-PO", "5.00")

	var supplierID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO suppliers (firm_id, name) VALUES ($1, 'Acme Supply Co') RETURNING id
	`, firmID).Scan(&supplierID); err != nil {
		t.Fatalf("seed supplier: %v", err)
	}

	definitionID, err := workflow.SeedPurchaseOrderWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedPurchaseOrderWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "po-stock-user",
		workflow.CreatePurchaseOrderPermission, workflow.SendPurchaseOrderPermission, workflow.ReceivePurchaseOrderPermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"supplier_id": supplierID.String(), "product_id": productID.String(),
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "send", nil); err != nil {
		t.Fatalf("ExecuteTransition send: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "receive", map[string]any{"quantity": float64(20)}); err != nil {
		t.Fatalf("ExecuteTransition receive: %v", err)
	}

	var remaining string
	if err := adminPool.QueryRow(ctx, `SELECT quantity::text FROM stock_levels WHERE product_id = $1`, productID).Scan(&remaining); err != nil {
		t.Fatalf("read stock level: %v", err)
	}
	if remaining != "20.0000" {
		t.Errorf("expected stock 20.0000, got %q", remaining)
	}

	var reason string
	if err := adminPool.QueryRow(ctx, `SELECT reason FROM stock_movements WHERE product_id = $1`, productID).Scan(&reason); err != nil {
		t.Fatalf("read stock movement: %v", err)
	}
	if reason != "purchase" {
		t.Errorf("expected reason %q, got %q", "purchase", reason)
	}
}

// TestCreateInstance_PurchaseOrderRejectsUnknownSupplier confirms
// FieldTypeSupplier's cross-module validation (checkSupplierField,
// engine.go) - the exact counterpart to FieldTypePerson's own validation
// (see task_approval_hr_integration_test.go), against `suppliers` instead
// of `people`.
func TestCreateInstance_PurchaseOrderRejectsUnknownSupplier(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm PO Bad Supplier') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	definitionID, err := workflow.SeedPurchaseOrderWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedPurchaseOrderWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "po-bad-supplier-user", workflow.CreatePurchaseOrderPermission)

	_, err = workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"supplier_id": uuid.New().String(),
	})
	if !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for an unknown supplier_id, got %v", err)
	}
}
