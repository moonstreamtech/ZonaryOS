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

// TestListInstancesByCustomer is Part 3c's own cross-link correctness
// test (multi-tenant isolation hardening + performance batch, "the first
// real CRM view that connects sales to customers"): a stock_to_sale
// instance whose payload.customer_id references a given customer comes
// back for that customer; an instance for a different customer, or one
// with no customer_id at all, does not.
func TestListInstancesByCustomer(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Customer Links Firm') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "customer-links-user", workflow.AddStockPermission)

	var customerAID, customerBID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO customers (firm_id, name, currency) VALUES ($1, 'Customer A', 'TRY') RETURNING id
	`, firmID).Scan(&customerAID); err != nil {
		t.Fatalf("seed customer A: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO customers (firm_id, name, currency) VALUES ($1, 'Customer B', 'TRY') RETURNING id
	`, firmID).Scan(&customerBID); err != nil {
		t.Fatalf("seed customer B: %v", err)
	}

	saleForA, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"customer_id": customerAID.String(),
	})
	if err != nil {
		t.Fatalf("CreateInstance (sale for A): %v", err)
	}
	if _, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"customer_id": customerBID.String(),
	}); err != nil {
		t.Fatalf("CreateInstance (sale for B): %v", err)
	}
	if _, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{}); err != nil {
		t.Fatalf("CreateInstance (no customer): %v", err)
	}

	salesForA, err := workflow.ListInstancesByCustomer(ctx, appPool, firmID, userID, customerAID)
	if err != nil {
		t.Fatalf("ListInstancesByCustomer (A): %v", err)
	}
	if len(salesForA) != 1 || salesForA[0].ID != saleForA {
		t.Fatalf("expected exactly the one sale for customer A, got %+v", salesForA)
	}
	if salesForA[0].DefinitionKey != workflow.StockToSaleKey {
		t.Errorf("expected definition key %q, got %q", workflow.StockToSaleKey, salesForA[0].DefinitionKey)
	}

	salesForUnrelated, err := workflow.ListInstancesByCustomer(ctx, appPool, firmID, userID, uuid.New())
	if err != nil {
		t.Fatalf("ListInstancesByCustomer (unrelated): %v", err)
	}
	if len(salesForUnrelated) != 0 {
		t.Fatalf("expected no sales for an unrelated customer id, got %+v", salesForUnrelated)
	}
}
