// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package invoicing_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/invoicing"
)

func TestBulkUpdateInvoiceStatus_AllSucceed(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Bulk Status Firm", "bulk-status-owner")

	var invoiceIDs []uuid.UUID
	for i := 0; i < 3; i++ {
		inv, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
			{Description: "Line", Quantity: "1", UnitPrice: "10.00"},
		})
		if err != nil {
			t.Fatalf("seed invoice %d: %v", i, err)
		}
		invoiceIDs = append(invoiceIDs, inv.ID)
	}

	updated, err := invoicing.BulkUpdateInvoiceStatus(ctx, appPool, firmID, ownerID, invoiceIDs, invoicing.InvoiceStatusSent)
	if err != nil {
		t.Fatalf("BulkUpdateInvoiceStatus: %v", err)
	}
	if len(updated) != 3 {
		t.Fatalf("expected 3 updated invoices, got %d", len(updated))
	}
	for _, inv := range updated {
		if inv.Status != invoicing.InvoiceStatusSent {
			t.Errorf("invoice %s: expected status sent, got %q", inv.ID, inv.Status)
		}
	}
}

// TestBulkUpdateInvoiceStatus_PartialFailureRollsBackAll is Part 4's own
// explicit atomicity requirement for the invoicing side: one invoice
// already 'cancelled' can never move to 'sent' (statusTransitionAllowed
// has no entry for that), so its presence in the batch must fail the
// WHOLE call and leave every other invoice in the batch untouched.
func TestBulkUpdateInvoiceStatus_PartialFailureRollsBackAll(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Bulk Status Firm", "bulk-status-owner")

	goodA, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Line", Quantity: "1", UnitPrice: "10.00"},
	})
	if err != nil {
		t.Fatalf("seed invoice goodA: %v", err)
	}
	goodB, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Line", Quantity: "1", UnitPrice: "10.00"},
	})
	if err != nil {
		t.Fatalf("seed invoice goodB: %v", err)
	}
	cancelled, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Line", Quantity: "1", UnitPrice: "10.00"},
	})
	if err != nil {
		t.Fatalf("seed invoice cancelled: %v", err)
	}
	if _, err := invoicing.UpdateInvoiceStatus(ctx, appPool, firmID, ownerID, cancelled.ID, invoicing.InvoiceStatusCancelled); err != nil {
		t.Fatalf("pre-cancel: %v", err)
	}

	_, err = invoicing.BulkUpdateInvoiceStatus(ctx, appPool, firmID, ownerID,
		[]uuid.UUID{goodA.ID, goodB.ID, cancelled.ID}, invoicing.InvoiceStatusSent)
	if !errors.Is(err, invoicing.ErrInvalidInvoice) {
		t.Fatalf("expected ErrInvalidInvoice, got %v", err)
	}

	for _, id := range []uuid.UUID{goodA.ID, goodB.ID} {
		got, err := invoicing.GetInvoice(ctx, appPool, firmID, ownerID, id)
		if err != nil {
			t.Fatalf("GetInvoice: %v", err)
		}
		if got.Status != invoicing.InvoiceStatusDraft {
			t.Errorf("invoice %s: expected the failed bulk call to leave it at draft, got %q", id, got.Status)
		}
	}
}

func TestBulkUpdateInvoiceStatus_EmptyRejected(t *testing.T) {
	_, appPool := setupTest(t)
	_, err := invoicing.BulkUpdateInvoiceStatus(context.Background(), appPool, uuid.New(), uuid.New(), nil, invoicing.InvoiceStatusSent)
	if !errors.Is(err, invoicing.ErrInvalidInvoice) {
		t.Fatalf("expected ErrInvalidInvoice, got %v", err)
	}
}
