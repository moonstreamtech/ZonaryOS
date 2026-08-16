// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package documents

import (
	"strings"
	"testing"
)

func TestRenderTemplate_KnownFields(t *testing.T) {
	tpl := "Invoice {{.Invoice.Number}} total {{.Invoice.Total}} {{.Invoice.Currency}}"
	data := map[string]any{
		"Invoice": map[string]any{
			"Number":   "INV-0001",
			"Total":    "123.45",
			"Currency": "TRY",
		},
	}
	out, err := renderTemplate(tpl, data)
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	want := "Invoice INV-0001 total 123.45 TRY"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestRenderTemplate_LinesRange(t *testing.T) {
	tpl := "{{range .Lines}}[{{.Description}}:{{.LineTotal}}]{{end}}"
	data := map[string]any{
		"Lines": []map[string]any{
			{"Description": "Widget", "LineTotal": "10.00"},
			{"Description": "Gadget", "LineTotal": "20.00"},
		},
	}
	out, err := renderTemplate(tpl, data)
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	want := "[Widget:10.00][Gadget:20.00]"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

// TestRenderTemplate_UnknownFieldRendersEmpty is the batch's explicit
// requirement: a template referencing a field that doesn't exist on the
// entity renders an empty string, not an error - see RenderInvoice's own
// doc comment on why (missingkey=zero over a map[string]any data
// context).
func TestRenderTemplate_UnknownFieldRendersEmpty(t *testing.T) {
	tpl := "Before[{{.Invoice.NoSuchField}}]After"
	data := map[string]any{
		"Invoice": map[string]any{"Number": "INV-0001"},
	}
	out, err := renderTemplate(tpl, data)
	if err != nil {
		t.Fatalf("renderTemplate should not error on an unknown field: %v", err)
	}
	if out != "Before[]After" {
		t.Fatalf("expected unknown field to render empty, got %q", out)
	}
}

func TestRenderTemplate_UnknownTopLevelKeyRendersEmpty(t *testing.T) {
	tpl := "[{{.DoesNotExist}}]"
	out, err := renderTemplate(tpl, map[string]any{})
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	if out != "[]" {
		t.Fatalf("expected empty, got %q", out)
	}
}

func TestRenderTemplate_DefaultInvoiceTemplate(t *testing.T) {
	data := map[string]any{
		"Invoice": map[string]any{
			"Number": "INV-0042", "IssuedDate": "2026-01-01", "DueDate": "2026-01-31",
			"Subtotal": "100.00", "TaxAmount": "20.00", "Total": "120.00", "Currency": "TRY",
		},
		"Lines": []map[string]any{
			{"Description": "Consulting", "Quantity": "1", "UnitPrice": "100.00", "LineTotal": "100.00"},
		},
		"Customer": map[string]any{"Name": "Acme Corp", "Address": "1 Main St"},
		"Firm":     map[string]any{"Name": "ZonaryOS Demo Firm", "Address": "2 Side St"},
	}
	out, err := renderTemplate(defaultInvoiceTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	for _, want := range []string{"INV-0042", "Acme Corp", "ZonaryOS Demo Firm", "Consulting", "120.00 TRY"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected rendered output to contain %q, got:\n%s", want, out)
		}
	}
}

// TestRenderTemplate_DefaultContractTemplate confirms the default
// contract (NDA-style) template's HTML output actually contains the
// contract fields from the template - same shape
// TestRenderTemplate_DefaultInvoiceTemplate uses above.
func TestRenderTemplate_DefaultContractTemplate(t *testing.T) {
	data := map[string]any{
		"Contract": map[string]any{
			"ReferenceNumber": "C-0042", "Title": "Non-Disclosure Agreement", "Status": "active",
			"StartDate": "2026-01-01", "EndDate": "2027-01-01", "Value": "0", "Currency": "TRY",
			"Notes": "Confidential information shall not be disclosed.",
		},
		"Counterparty": map[string]any{"Name": "Acme Corp"},
		"Firm":         map[string]any{"Name": "ZonaryOS Demo Firm", "Address": "2 Side St"},
	}
	out, err := renderTemplate(defaultContractTemplate, data)
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	for _, want := range []string{"C-0042", "Non-Disclosure Agreement", "Acme Corp", "ZonaryOS Demo Firm", "Confidential information shall not be disclosed."} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected rendered output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestValidTemplateType(t *testing.T) {
	for _, typ := range []TemplateType{TemplateTypeInvoice, TemplateTypeDeliveryNote, TemplateTypeReport, TemplateTypeContract} {
		if !validTemplateType(typ) {
			t.Fatalf("expected %q to be a valid template type", typ)
		}
	}
	if validTemplateType("bogus") {
		t.Fatal("expected an unknown template type to be invalid")
	}
}
