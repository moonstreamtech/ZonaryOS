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

// TestExecuteTransition_RecordSaleCreatesInvoice is the workflow-to-
// invoicing bridge's own working example: recording a sale with both
// quantity and unit_price present auto-creates a real draft invoice (with
// one line), in the same transaction as the state change, ledger entry,
// stock adjustment, and delivery - "the accounting, customer, and sales
// modules now have enough data to generate real invoices" (design brief).
func TestExecuteTransition_RecordSaleCreatesInvoice(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Invoicing') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	seedLedgerAccounts(ctx, t, adminPool, firmID)

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "invoicing-user", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", map[string]any{
		"quantity": float64(3), "unit_price": float64(20),
	}); err != nil {
		t.Fatalf("ExecuteTransition record_sale: %v", err)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count invoices: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one invoice, got %d", count)
	}

	var status, invoiceNumber, subtotal, total, sourceWorkflowInstance string
	if err := adminPool.QueryRow(ctx, `
		SELECT status, invoice_number, subtotal::text, total::text, source_workflow_instance::text
		FROM invoices WHERE firm_id = $1
	`, firmID).Scan(&status, &invoiceNumber, &subtotal, &total, &sourceWorkflowInstance); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if status != "draft" {
		t.Errorf("expected a bridge-created invoice to start draft, got %q", status)
	}
	if invoiceNumber != "INV-0001" {
		t.Errorf("expected INV-0001, got %q", invoiceNumber)
	}
	// 3 * 20.00 = 60.0000, no tax_rate supplied.
	if subtotal != "60.0000" || total != "60.0000" {
		t.Errorf("expected subtotal/total 60.0000, got subtotal=%q total=%q", subtotal, total)
	}
	if sourceWorkflowInstance != instanceID.String() {
		t.Errorf("expected source_workflow_instance to trace back to the sale instance, got %q", sourceWorkflowInstance)
	}

	var description, quantity, unitPrice string
	if err := adminPool.QueryRow(ctx, `
		SELECT il.description, il.quantity::text, il.unit_price::text
		FROM invoice_lines il JOIN invoices i ON i.id = il.invoice_id
		WHERE i.firm_id = $1
	`, firmID).Scan(&description, &quantity, &unitPrice); err != nil {
		t.Fatalf("read invoice line: %v", err)
	}
	if description != "Sale of widget" {
		t.Errorf("expected the line description to interpolate {{item}}, got %q", description)
	}
	if quantity != "3.0000" || unitPrice != "20.0000" {
		t.Errorf("expected quantity=3.0000 unitPrice=20.0000, got quantity=%q unitPrice=%q", quantity, unitPrice)
	}
}

// TestExecuteTransition_RecordSaleWithoutPriceSkipsInvoice is the Rule 6
// regression test: record_sale executed with no quantity/unit_price in
// the payload (every ExecuteTransition("record_sale", ...) call site that
// predates this batch) must keep succeeding, and must not create an
// invoice at all.
func TestExecuteTransition_RecordSaleWithoutPriceSkipsInvoice(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Invoicing 2') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "invoicing-user-2", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", nil); err != nil {
		t.Fatalf("ExecuteTransition record_sale (no quantity/unit_price): %v", err)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count invoices: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no invoices without quantity/unit_price, got %d", count)
	}
}

// TestExecuteTransition_RecordSaleInvoiceIncludesCustomer confirms the
// bridge-created invoice's customer_id is set from the sale's own
// customer_id payload field (InvoiceTemplate.CustomerField reusing the
// same field stock_to_sale's Delivery/Journal-description features
// already read), not a new field.
func TestExecuteTransition_RecordSaleInvoiceIncludesCustomer(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Invoicing Customer') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	seedLedgerAccounts(ctx, t, adminPool, firmID)

	var customerID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO customers (firm_id, name) VALUES ($1, 'Wayne Enterprises') RETURNING id
	`, firmID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "invoicing-customer-user", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"item": "widget", "customer_id": customerID.String(),
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", map[string]any{
		"quantity": float64(1), "unit_price": float64(50),
	}); err != nil {
		t.Fatalf("ExecuteTransition record_sale: %v", err)
	}

	var invoiceCustomerID uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT customer_id FROM invoices WHERE firm_id = $1`, firmID).Scan(&invoiceCustomerID); err != nil {
		t.Fatalf("read invoice customer_id: %v", err)
	}
	if invoiceCustomerID != customerID {
		t.Errorf("expected invoice.customer_id %v, got %v", customerID, invoiceCustomerID)
	}
}
