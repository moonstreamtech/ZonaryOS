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

	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// TestExecuteTransition_RecordSaleCreatesDelivery is the workflow-to-
// logistics bridge's own working example: recording a sale with
// destination_address present creates a real deliveries row, in the same
// transaction as the state change - "selling a product can automatically
// create a delivery record" (design brief).
func TestExecuteTransition_RecordSaleCreatesDelivery(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Logistics') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "logistics-user", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", map[string]any{
		"destination_address": "123 Main St", "carrier": "Acme Shipping", "tracking_number": "TRACK123",
	}); err != nil {
		t.Fatalf("ExecuteTransition record_sale: %v", err)
	}

	var count int
	var destination, carrier, tracking, sourceType string
	var sourceID uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM deliveries WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one delivery, got %d", count)
	}
	if err := adminPool.QueryRow(ctx, `
		SELECT destination_address, carrier, tracking_number, source_type, source_id FROM deliveries WHERE firm_id = $1
	`, firmID).Scan(&destination, &carrier, &tracking, &sourceType, &sourceID); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if destination != "123 Main St" || carrier != "Acme Shipping" || tracking != "TRACK123" {
		t.Errorf("unexpected delivery fields: destination=%q carrier=%q tracking=%q", destination, carrier, tracking)
	}
	if sourceType != "workflow_instance" || sourceID != instanceID {
		t.Errorf("expected the delivery to trace back to the workflow instance, got sourceType=%q sourceId=%v", sourceType, sourceID)
	}
}

// TestExecuteTransition_RecordSaleWithoutDestinationSkipsDelivery is the
// Rule 6 regression test: record_sale executed with no destination_address
// in the payload (every ExecuteTransition("record_sale", ...) call site
// that predates this batch) must keep succeeding, and must not create a
// delivery at all.
func TestExecuteTransition_RecordSaleWithoutDestinationSkipsDelivery(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Logistics 2') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "logistics-user-2", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", nil); err != nil {
		t.Fatalf("ExecuteTransition record_sale (no destination): %v", err)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM deliveries WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no deliveries without a destination address, got %d", count)
	}
}

// TestExecuteTransition_ConvertCreatesCustomer is the workflow-to-CRM
// bridge's own working example: converting a qualified lead with name
// present creates a real customers row, with source_workflow_instance set
// to the lead's own instance, in the same transaction as the state
// change - "when a lead converts to customer via a transition, auto-create
// a customer record" (design brief).
func TestExecuteTransition_ConvertCreatesCustomer(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm CRM') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	definitionID, err := workflow.SeedCustomerPipelineWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedCustomerPipelineWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "crm-user",
		workflow.CreateLeadPermission, workflow.QualifyLeadPermission, workflow.ConvertCustomerPermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"name": "Wayne Enterprises", "email": "bruce@wayne.example",
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "qualify", nil); err != nil {
		t.Fatalf("ExecuteTransition qualify: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "convert", nil); err != nil {
		t.Fatalf("ExecuteTransition convert: %v", err)
	}

	var count int
	var name, email string
	var sourceInstance uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count customers: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one customer, got %d", count)
	}
	if err := adminPool.QueryRow(ctx, `
		SELECT name, email, source_workflow_instance FROM customers WHERE firm_id = $1
	`, firmID).Scan(&name, &email, &sourceInstance); err != nil {
		t.Fatalf("read customer: %v", err)
	}
	if name != "Wayne Enterprises" || email != "bruce@wayne.example" {
		t.Errorf("unexpected customer fields: name=%q email=%q", name, email)
	}
	if sourceInstance != instanceID {
		t.Errorf("expected source_workflow_instance to trace back to the lead's instance, got %v", sourceInstance)
	}
}

// TestExecuteTransition_ConvertWithoutNameSkipsCustomer is the Rule 6
// regression test: converting a lead with no name in the payload (every
// ExecuteTransition("convert", ...) call site that predates this batch,
// e.g. this package's own pre-existing customer_pipeline tests) must keep
// succeeding, and must not create a customer at all.
func TestExecuteTransition_ConvertWithoutNameSkipsCustomer(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm CRM 2') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	definitionID, err := workflow.SeedCustomerPipelineWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedCustomerPipelineWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "crm-user-2",
		workflow.CreateLeadPermission, workflow.QualifyLeadPermission, workflow.ConvertCustomerPermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "qualify", nil); err != nil {
		t.Fatalf("ExecuteTransition qualify: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "convert", nil); err != nil {
		t.Fatalf("ExecuteTransition convert (no name): %v", err)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count customers: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no customers without a name, got %d", count)
	}
}

// TestCreateInstance_StockToSaleRejectsUnknownCustomer confirms
// FieldTypeCustomer's cross-module validation on stock_to_sale's own
// customer_id field (checkCustomerField, engine.go) - the exact
// counterpart to FieldTypeSupplier's own validation on purchase_order.
func TestCreateInstance_StockToSaleRejectsUnknownCustomer(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Bad Customer') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "bad-customer-user", workflow.AddStockPermission)

	_, err = workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"customer_id": uuid.New().String(),
	})
	if !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for an unknown customer_id, got %v", err)
	}
}

// TestExecuteTransition_RecordSaleWithCustomerIncludesNameInDescription
// confirms resolveJournalDescription's own working example: a sale
// referencing a real customer_id gets that customer's name appended to
// the posted journal entry's description, via the SAME {{field}}
// interpolation mechanism JournalTemplate.Description already uses for
// "{{item}}" - see engine.go's resolveJournalDescription.
func TestExecuteTransition_RecordSaleWithCustomerIncludesNameInDescription(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Sale Customer') RETURNING id`).Scan(&firmID); err != nil {
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
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sale-customer-user", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"item": "widget", "customer_id": customerID.String(),
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", map[string]any{
		"quantity": float64(1), "unit_price": float64(10),
	}); err != nil {
		t.Fatalf("ExecuteTransition record_sale: %v", err)
	}

	var description string
	if err := adminPool.QueryRow(ctx, `
		SELECT description FROM journal_entries WHERE firm_id = $1 AND source_id = $2
	`, firmID, instanceID).Scan(&description); err != nil {
		t.Fatalf("read journal entry: %v", err)
	}
	const want = "Sale of widget (customer: Wayne Enterprises)"
	if description != want {
		t.Errorf("expected description %q, got %q", want, description)
	}
}
