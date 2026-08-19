// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package reports_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/reports"
)

// TestCohortAnalysis_CustomerAppearsInCorrectCohortCells is Part 5's own
// required test: a customer created in month 1 with invoices in months
// 1-3 appears in the correct cohort cells (period 0/1/2 of their own
// cohort row), and a later invoice in a period beyond ?periods= is
// excluded rather than silently included in the wrong bucket.
func TestCohortAnalysis_CustomerAppearsInCorrectCohortCells(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, ownerID, _ := seedOwner(ctx, t, adminPool, appPool, "Cohort Firm", "cohort-owner")

	cohortMonth := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var customerID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO customers (firm_id, name, currency, created_at) VALUES ($1, $2, 'TRY', $3) RETURNING id
	`, firmID, "Cohort Customer", cohortMonth.AddDate(0, 0, 15)).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	seedInvoice := func(number string, issuedDate time.Time, total string) {
		t.Helper()
		if _, err := adminPool.Exec(ctx, `
			INSERT INTO invoices (firm_id, invoice_number, customer_id, issued_date, status, total, currency)
			VALUES ($1, $2, $3, $4, 'sent', $5::numeric, 'TRY')
		`, firmID, number, customerID, issuedDate, total); err != nil {
			t.Fatalf("seed invoice %s: %v", number, err)
		}
	}
	// Period 0 (cohort month itself): 100. Period 1 (one month later): 50.
	// Period 2 (two months later): 25. Period 6 (beyond periods=6, i.e.
	// index 6 when periods=6 only has indices 0-5): must be excluded.
	seedInvoice("INV-1", cohortMonth.AddDate(0, 0, 20), "100.00")
	seedInvoice("INV-2", cohortMonth.AddDate(0, 1, 5), "50.00")
	seedInvoice("INV-3", cohortMonth.AddDate(0, 2, 10), "25.00")
	seedInvoice("INV-4", cohortMonth.AddDate(0, 6, 1), "9999.00")

	table, err := reports.CohortAnalysis(ctx, appPool, firmID, ownerID, reports.CohortSpec{
		Entity: "customers", CohortBy: "created_at", Metric: "invoice_total", Periods: 6,
	})
	if err != nil {
		t.Fatalf("CohortAnalysis: %v", err)
	}
	if table.Periods != 6 {
		t.Fatalf("expected 6 periods, got %d", table.Periods)
	}
	if len(table.Cohorts) != 1 {
		t.Fatalf("expected exactly one cohort row, got %+v", table.Cohorts)
	}
	row := table.Cohorts[0]
	if !row.CohortMonth.Equal(cohortMonth) {
		t.Fatalf("expected cohort month %v, got %v", cohortMonth, row.CohortMonth)
	}
	if row.CohortSize != 1 {
		t.Fatalf("expected cohort size 1, got %d", row.CohortSize)
	}
	if len(row.Values) != 6 {
		t.Fatalf("expected 6 value slots, got %d", len(row.Values))
	}
	assertValue := func(period int, want float64) {
		t.Helper()
		if row.Values[period] == nil {
			t.Fatalf("expected period %d to have a value, got nil", period)
		}
		if *row.Values[period] != want {
			t.Fatalf("expected period %d = %v, got %v", period, want, *row.Values[period])
		}
	}
	assertValue(0, 100.0)
	assertValue(1, 50.0)
	assertValue(2, 25.0)
	if row.Values[3] != nil {
		t.Fatalf("expected period 3 to have no data (nil), got %v", *row.Values[3])
	}
	// INV-4 (period 6) must never show up anywhere in a periods=6 table
	// (valid indices 0-5) - re-derive the total of everything actually
	// counted and confirm it excludes INV-4's own 9999.00.
	var total float64
	for _, v := range row.Values {
		if v != nil {
			total += *v
		}
	}
	if total != 175.0 {
		t.Fatalf("expected total counted across periods 0-5 to be 175.0 (100+50+25), got %v - INV-4 leaked into the table", total)
	}
}

// TestCohortAnalysis_UnsupportedSpecRejected covers this batch's own
// "only entity=customers cohort_by=created_at metric=invoice_total is
// implemented" scope boundary - any other combination is a clear 400,
// never a silent no-op.
func TestCohortAnalysis_UnsupportedSpecRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, ownerID, _ := seedOwner(ctx, t, adminPool, appPool, "Cohort Unsupported Firm", "cohort-unsupported-owner")

	_, err := reports.CohortAnalysis(ctx, appPool, firmID, ownerID, reports.CohortSpec{
		Entity: "products", CohortBy: "created_at", Metric: "invoice_total", Periods: 6,
	})
	if !errors.Is(err, reports.ErrCohortSpecNotSupported) {
		t.Fatalf("expected ErrCohortSpecNotSupported, got %v", err)
	}
}
