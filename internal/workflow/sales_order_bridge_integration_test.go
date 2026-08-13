// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// TestExecuteTransition_RecordSaleCreatesSalesOrder is the workflow-to-
// sales-order bridge's own working example (sixth bridge, sales orders +
// full procurement cycle batch): recording a sale with both quantity and
// unit_price present auto-creates a real, CONFIRMED sales order (with one
// line), in the SAME transaction as the state change, ledger entry, stock
// adjustment, delivery, and invoice - mirrors
// TestExecuteTransition_RecordSaleCreatesInvoice's own shape for the fifth
// bridge.
func TestExecuteTransition_RecordSaleCreatesSalesOrder(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Sales Order') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	seedLedgerAccounts(ctx, t, adminPool, firmID)

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sales-order-user", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", map[string]any{
		"quantity": float64(4), "unit_price": float64(25), "destination_address": "1 Main St",
	}); err != nil {
		t.Fatalf("ExecuteTransition record_sale: %v", err)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM sales_orders WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count sales orders: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one sales order, got %d", count)
	}

	var status, orderNumber, subtotal, total, shippingAddress, sourceWorkflowInstance string
	if err := adminPool.QueryRow(ctx, `
		SELECT status, order_number, subtotal::text, total::text, shipping_address, source_workflow_instance::text
		FROM sales_orders WHERE firm_id = $1
	`, firmID).Scan(&status, &orderNumber, &subtotal, &total, &shippingAddress, &sourceWorkflowInstance); err != nil {
		t.Fatalf("read sales order: %v", err)
	}
	if status != "confirmed" {
		t.Errorf("expected a bridge-created sales order to start confirmed, got %q", status)
	}
	if orderNumber != "SO-0001" {
		t.Errorf("expected SO-0001, got %q", orderNumber)
	}
	// 4 * 25.00 = 100.0000, no tax_rate supplied.
	if subtotal != "100.0000" || total != "100.0000" {
		t.Errorf("expected subtotal/total 100.0000, got subtotal=%q total=%q", subtotal, total)
	}
	if shippingAddress != "1 Main St" {
		t.Errorf("expected shipping_address to resolve from destination_address, got %q", shippingAddress)
	}
	if sourceWorkflowInstance != instanceID.String() {
		t.Errorf("expected source_workflow_instance to trace back to the sale instance, got %q", sourceWorkflowInstance)
	}

	var description, quantity, unitPrice string
	if err := adminPool.QueryRow(ctx, `
		SELECT sol.description, sol.quantity::text, sol.unit_price::text
		FROM sales_order_lines sol JOIN sales_orders so ON so.id = sol.order_id
		WHERE so.firm_id = $1
	`, firmID).Scan(&description, &quantity, &unitPrice); err != nil {
		t.Fatalf("read sales order line: %v", err)
	}
	if description != "Sale of widget" {
		t.Errorf("expected the line description to interpolate {{item}}, got %q", description)
	}
	if quantity != "4.0000" || unitPrice != "25.0000" {
		t.Errorf("expected quantity=4.0000 unitPrice=25.0000, got quantity=%q unitPrice=%q", quantity, unitPrice)
	}
}

// TestExecuteTransition_RecordSaleWithoutPriceSkipsSalesOrder is the Rule
// 6 regression test: record_sale executed with no quantity/unit_price in
// the payload must keep succeeding, and must not create a sales order at
// all - same "skip, not fail" contract
// TestExecuteTransition_RecordSaleWithoutPriceSkipsInvoice already
// establishes for the invoicing bridge.
func TestExecuteTransition_RecordSaleWithoutPriceSkipsSalesOrder(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Sales Order 2') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sales-order-user-2", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", nil); err != nil {
		t.Fatalf("ExecuteTransition record_sale (no quantity/unit_price): %v", err)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM sales_orders WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count sales orders: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no sales orders without quantity/unit_price, got %d", count)
	}
}

// TestExecuteTransition_SendPurchaseOrderCreatesPurchaseOrder is the
// workflow-to-purchase-order bridge's own working example (seventh
// bridge): sending a draft purchase order with both quantity and
// unit_price present auto-creates a real, 'sent' purchase order (with one
// line), sourced to the instance.
func TestExecuteTransition_SendPurchaseOrderCreatesPurchaseOrder(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Purchase Order') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	definitionID, err := workflow.SeedPurchaseOrderWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedPurchaseOrderWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "purchase-order-user",
		workflow.CreatePurchaseOrderPermission, workflow.SendPurchaseOrderPermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"supplier_name": "Acme Supplies"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "send", map[string]any{
		"quantity": float64(10), "unit_price": float64(5),
	}); err != nil {
		t.Fatalf("ExecuteTransition send: %v", err)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM purchase_orders WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count purchase orders: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one purchase order, got %d", count)
	}

	var status, orderNumber, total, sourceWorkflowInstance string
	if err := adminPool.QueryRow(ctx, `
		SELECT status, order_number, total::text, source_workflow_instance::text
		FROM purchase_orders WHERE firm_id = $1
	`, firmID).Scan(&status, &orderNumber, &total, &sourceWorkflowInstance); err != nil {
		t.Fatalf("read purchase order: %v", err)
	}
	if status != "sent" {
		t.Errorf("expected a bridge-created purchase order to start sent, got %q", status)
	}
	if orderNumber != "PO-0001" {
		t.Errorf("expected PO-0001, got %q", orderNumber)
	}
	// 10 * 5.00 = 50.0000.
	if total != "50.0000" {
		t.Errorf("expected total 50.0000, got %q", total)
	}
	if sourceWorkflowInstance != instanceID.String() {
		t.Errorf("expected source_workflow_instance to trace back to the PO instance, got %q", sourceWorkflowInstance)
	}
}
