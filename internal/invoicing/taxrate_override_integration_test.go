// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package invoicing_test

import (
	"context"
	"testing"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/invoicing"
	"github.com/moonstreamtech/ZonaryOS/internal/localization"
)

// TestAddInvoiceLine_TaxRateIDOverridesTaxRate confirms the multi-batch
// wire-in (this batch, migrations/0020's invoice_lines.tax_rate_id):
// when a line references a firm's own tax_rates catalog entry, the
// referenced rate (a fraction, e.g. 0.2000) is resolved and converted to
// invoice_lines.tax_rate's percentage shape (20.00) at write time,
// overriding whatever plain TaxRate string was also supplied.
// setupTest/seedOwner are shared with invoicing_integration_test.go
// (same package).
func TestAddInvoiceLine_TaxRateIDOverridesTaxRate(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm Tax A", "sub-tax-a")

	taxRate, err := localization.CreateTaxRate(ctx, appPool, firmID, ownerID, localization.TaxRateInput{
		Name: "Standard VAT", Rate: "0.2000", CountryCode: "TR",
	})
	if err != nil {
		t.Fatalf("CreateTaxRate: %v", err)
	}

	issuedDate := time.Now()
	inv, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, issuedDate, nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Widget", Quantity: "1", UnitPrice: "100.00", TaxRate: "5.00" /* ignored, TaxRateID overrides it */, TaxRateID: &taxRate.ID},
	})
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	if len(inv.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(inv.Lines))
	}
	line := inv.Lines[0]
	if line.TaxRateID == nil || *line.TaxRateID != taxRate.ID {
		t.Fatalf("expected the line's taxRateId to be %s, got %+v", taxRate.ID, line.TaxRateID)
	}
	// 0.2000 (fraction) -> 20.00 (percentage, rounded to
	// invoice_lines.tax_rate's own numeric(5,2) column scale), NOT the
	// plain "5.00" TaxRate that was also supplied on the input.
	if line.TaxRate != "20.00" {
		t.Fatalf("expected the resolved tax rate to be 20.00 (0.2000 * 100), got %q", line.TaxRate)
	}

	// tax_amount = line_total (100.00) * tax_rate (20.00) / 100 = 20.00
	if inv.TaxAmount != "20.0000" {
		t.Fatalf("expected invoice tax amount 20.0000, got %q", inv.TaxAmount)
	}
}

// TestAddInvoiceLine_PlainTaxRateStillWorksWithoutTaxRateID confirms the
// pre-existing plain-TaxRate path is completely unchanged when
// TaxRateID is left unset.
func TestAddInvoiceLine_PlainTaxRateStillWorksWithoutTaxRateID(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm Tax B", "sub-tax-b")

	issuedDate := time.Now()
	inv, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, issuedDate, nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Widget", Quantity: "1", UnitPrice: "100.00", TaxRate: "18.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	if len(inv.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(inv.Lines))
	}
	line := inv.Lines[0]
	if line.TaxRateID != nil {
		t.Fatalf("expected no taxRateId when none was supplied, got %+v", line.TaxRateID)
	}
	if line.TaxRate != "18.00" {
		t.Fatalf("expected the plain tax rate 18.00 unchanged, got %q", line.TaxRate)
	}
}
