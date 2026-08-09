// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package reports_test

import (
	"context"
	"errors"
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/reports"
)

// TestReportDefinitions_CreateListGetRun exercises the full parametric
// reporting engine loop: save a query_spec-backed definition, list it
// back, fetch it, and run it - RunReport's own generated SQL must produce
// correct, real results against seeded data (an invoice count grouped by
// status), and must record the run in saved_report_runs (indirectly
// verified via RunReport not erroring on the insert/update).
func TestReportDefinitions_CreateListGetRun(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID, _ := seedOwner(ctx, t, adminPool, appPool, "Report Firm", "report-owner")

	if _, err := adminPool.Exec(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, issued_date, status, subtotal, tax_amount, total, currency)
		VALUES
			($1, 'INV-0001', now(), 'sent', 100, 0, 100, 'TRY'),
			($1, 'INV-0002', now(), 'sent', 200, 0, 200, 'TRY'),
			($1, 'INV-0003', now(), 'paid', 50, 0, 50, 'TRY')
	`, firmID); err != nil {
		t.Fatalf("seed invoices: %v", err)
	}

	spec := reports.QuerySpec{
		Entity:  "invoices",
		GroupBy: "status",
		Metrics: []reports.QueryMetric{
			{Name: "count", Aggregation: "count"},
			{Name: "totalAmount", Aggregation: "sum(total)"},
		},
	}

	def, err := reports.CreateReportDefinition(ctx, appPool, firmID, userID, reports.ReportDefinitionInput{
		Name: "Invoices by status", QuerySpec: spec,
	})
	if err != nil {
		t.Fatalf("CreateReportDefinition: %v", err)
	}
	if def.Name != "Invoices by status" {
		t.Fatalf("unexpected name: %q", def.Name)
	}

	defs, err := reports.ListReportDefinitions(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("ListReportDefinitions: %v", err)
	}
	if len(defs) != 1 || defs[0].ID != def.ID {
		t.Fatalf("expected exactly the created definition back, got %+v", defs)
	}

	got, err := reports.GetReportDefinition(ctx, appPool, firmID, userID, def.ID)
	if err != nil {
		t.Fatalf("GetReportDefinition: %v", err)
	}
	if got.QuerySpec.Entity != "invoices" {
		t.Fatalf("unexpected query spec round trip: %+v", got.QuerySpec)
	}

	rows, err := reports.RunReport(ctx, appPool, firmID, userID, def.ID)
	if err != nil {
		t.Fatalf("RunReport: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 grouped rows (sent, paid), got %d: %+v", len(rows), rows)
	}
	byGroup := map[string]reports.ReportRow{}
	for _, r := range rows {
		if r.GroupKey == nil {
			t.Fatalf("expected a group key on every row, got %+v", r)
		}
		byGroup[*r.GroupKey] = r
	}
	sentCount := byGroup["sent"].Metrics["count"]
	if sentCount != int64(2) {
		t.Fatalf("expected 2 sent invoices, got %#v (row: %+v)", sentCount, byGroup["sent"])
	}
}

// TestReportDefinitions_InvalidQuerySpecRejected is the API-level
// counterpart of queryspec_test.go's unit tests: an invalid query_spec
// must be rejected by CreateReportDefinition itself (never saved), with
// ErrInvalidQuerySpec, so a bad definition never reaches the point where
// running it could fail or disclose information.
func TestReportDefinitions_InvalidQuerySpecRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID, _ := seedOwner(ctx, t, adminPool, appPool, "Report Firm", "report-owner")

	_, err := reports.CreateReportDefinition(ctx, appPool, firmID, userID, reports.ReportDefinitionInput{
		Name: "Bogus",
		QuerySpec: reports.QuerySpec{
			Entity:  "not_a_real_entity",
			Metrics: []reports.QueryMetric{{Name: "count", Aggregation: "count"}},
		},
	})
	if !errors.Is(err, reports.ErrInvalidQuerySpec) {
		t.Fatalf("expected ErrInvalidQuerySpec, got %v", err)
	}

	defs, err := reports.ListReportDefinitions(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("ListReportDefinitions: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("expected the invalid definition to never be saved, got %+v", defs)
	}
}

func TestReportDefinitions_NotFound(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID, _ := seedOwner(ctx, t, adminPool, appPool, "Report Firm", "report-owner")

	_, err := reports.GetReportDefinition(ctx, appPool, firmID, userID, firmID)
	if !errors.Is(err, reports.ErrReportDefinitionNotFound) {
		t.Fatalf("expected ErrReportDefinitionNotFound, got %v", err)
	}
}
