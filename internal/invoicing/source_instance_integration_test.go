// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package invoicing_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/invoicing"
)

// TestListInvoicesBySourceInstance is Part 3a's own cross-link
// correctness test (multi-tenant isolation hardening + performance
// batch): an invoice whose source_workflow_instance matches the given
// instance id comes back; an invoice for a different instance, or one
// with no source instance at all, does not.
func TestListInvoicesBySourceInstance(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Source Instance Firm", "source-instance-owner")

	// A minimal real workflow_instances row - direct SQL, same "bypass
	// the owning module's own gated API when only the FK target needs to
	// exist" convention definitions_integration_test.go's own seeding
	// already uses elsewhere in this codebase for similarly out-of-scope
	// fixtures.
	var definitionID, stateID, instanceAID, instanceBID uuid.UUID
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO permissions (key, description) VALUES ('workflow.test_def.create', 'Test Def create permission')
	`); err != nil {
		t.Fatalf("seed permissions: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO workflow_definitions (firm_id, key, name, create_permission_key)
		VALUES ($1, 'test_def', 'Test Def', 'workflow.test_def.create') RETURNING id
	`, firmID).Scan(&definitionID); err != nil {
		t.Fatalf("seed workflow_definitions: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO workflow_states (firm_id, workflow_definition_id, key, name, is_initial) VALUES ($1, $2, 'open', 'Open', true) RETURNING id
	`, firmID, definitionID).Scan(&stateID); err != nil {
		t.Fatalf("seed workflow_states: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO workflow_instances (firm_id, workflow_definition_id, current_state_id, created_by_user_id)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, firmID, definitionID, stateID, ownerID).Scan(&instanceAID); err != nil {
		t.Fatalf("seed workflow_instances A: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO workflow_instances (firm_id, workflow_definition_id, current_state_id, created_by_user_id)
		VALUES ($1, $2, $3, $4) RETURNING id
	`, firmID, definitionID, stateID, ownerID).Scan(&instanceBID); err != nil {
		t.Fatalf("seed workflow_instances B: %v", err)
	}

	if _, err := adminPool.Exec(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, issued_date, status, subtotal, tax_amount, total, currency, source_workflow_instance)
		VALUES ($1, 'INV-A1', now(), 'draft', 10, 0, 10, 'TRY', $2)
	`, firmID, instanceAID); err != nil {
		t.Fatalf("seed invoice for instance A: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, issued_date, status, subtotal, tax_amount, total, currency, source_workflow_instance)
		VALUES ($1, 'INV-B1', now(), 'draft', 20, 0, 20, 'TRY', $2)
	`, firmID, instanceBID); err != nil {
		t.Fatalf("seed invoice for instance B: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, issued_date, status, subtotal, tax_amount, total, currency)
		VALUES ($1, 'INV-NONE', now(), 'draft', 30, 0, 30, 'TRY')
	`, firmID); err != nil {
		t.Fatalf("seed invoice with no source instance: %v", err)
	}

	invoicesForA, err := invoicing.ListInvoicesBySourceInstance(ctx, appPool, firmID, ownerID, instanceAID)
	if err != nil {
		t.Fatalf("ListInvoicesBySourceInstance (A): %v", err)
	}
	if len(invoicesForA) != 1 || invoicesForA[0].InvoiceNumber != "INV-A1" {
		t.Fatalf("expected exactly INV-A1 for instance A, got %+v", invoicesForA)
	}

	invoicesForB, err := invoicing.ListInvoicesBySourceInstance(ctx, appPool, firmID, ownerID, instanceBID)
	if err != nil {
		t.Fatalf("ListInvoicesBySourceInstance (B): %v", err)
	}
	if len(invoicesForB) != 1 || invoicesForB[0].InvoiceNumber != "INV-B1" {
		t.Fatalf("expected exactly INV-B1 for instance B, got %+v", invoicesForB)
	}

	invoicesForUnrelated, err := invoicing.ListInvoicesBySourceInstance(ctx, appPool, firmID, ownerID, uuid.New())
	if err != nil {
		t.Fatalf("ListInvoicesBySourceInstance (unrelated): %v", err)
	}
	if len(invoicesForUnrelated) != 0 {
		t.Fatalf("expected no invoices for an unrelated instance id, got %+v", invoicesForUnrelated)
	}
}
