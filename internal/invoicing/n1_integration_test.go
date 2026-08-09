// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package invoicing_test

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/invoicing"
)

// countingTracer counts every query pgx actually sends to Postgres - used
// below to prove ListInvoicesWithLines makes a FIXED number of round
// trips (2: one for invoices, one for every invoice's lines batched via
// `= ANY($1)`) regardless of how many invoices exist, not one extra
// round trip per invoice (the N+1 this package's own ListInvoicesWithLines
// doc comment says it fixes - see docs/DEVELOPMENT.md's "Performance:
// N+1 query audit" section for the full writeup).
type countingTracer struct {
	count atomic.Int64
}

func (c *countingTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.count.Add(1)
	return ctx
}
func (c *countingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// openTracedPool opens a second pool against the same app DSN setupTest
// already migrated/truncated against, with a query-counting tracer
// attached - zdb.Open itself has no tracer hook, so this constructs the
// pgxpool.Config directly (same DSN, same RLS-enforced app role).
func openTracedPool(t *testing.T, appDSN string) (*pgxpool.Pool, *countingTracer) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(appDSN)
	if err != nil {
		t.Fatalf("parse app DSN: %v", err)
	}
	tracer := &countingTracer{}
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open traced pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, tracer
}

func TestListInvoicesWithLines_FixedRoundTripsRegardlessOfInvoiceCount(t *testing.T) {
	adminDSN := os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN := os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set")
	}

	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "N1 Firm", "n1-owner")

	// Seed 5 invoices, each with 2 lines - if ListInvoicesWithLines still
	// had the old GetInvoice-per-invoice shape, this would cost 1 (list)
	// + 5 (one GetInvoice call each, which itself is 2 queries: the
	// invoice row + its lines) = 11 round trips; batched, it must always
	// be exactly 2, regardless of N.
	for i := 0; i < 5; i++ {
		inv, err := invoicing.CreateInvoice(ctx, appPool, firmID, userID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
			{Description: "Line A", Quantity: "1", UnitPrice: "10.00"},
			{Description: "Line B", Quantity: "2", UnitPrice: "5.00"},
		})
		if err != nil {
			t.Fatalf("seed invoice %d: %v", i, err)
		}
		if len(inv.Lines) != 2 {
			t.Fatalf("expected 2 seeded lines, got %d", len(inv.Lines))
		}
	}

	tracedPool, tracer := openTracedPool(t, appDSN)
	// zdb.WithFirmContext itself issues a couple of queries (SET
	// config, commit) - measure the delta ListInvoicesWithLines itself
	// adds, not the transaction wrapper's own fixed overhead.
	before := tracer.count.Load()

	invoices, err := invoicing.ListInvoicesWithLines(ctx, tracedPool, firmID, userID)
	if err != nil {
		t.Fatalf("ListInvoicesWithLines: %v", err)
	}
	if len(invoices) != 5 {
		t.Fatalf("expected 5 invoices, got %d", len(invoices))
	}
	for _, inv := range invoices {
		if len(inv.Lines) != 2 {
			t.Fatalf("invoice %s: expected 2 lines, got %d", inv.ID, len(inv.Lines))
		}
	}

	queriesForFiveInvoices := tracer.count.Load() - before

	// Re-run against a firm with a different invoice count to prove the
	// round-trip count doesn't scale with N (the actual N+1 signature).
	firmID2, userID2 := seedOwner(ctx, t, adminPool, appPool, "N1 Firm 2", "n1-owner-2")
	for i := 0; i < 20; i++ {
		if _, err := invoicing.CreateInvoice(ctx, appPool, firmID2, userID2, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
			{Description: "Solo line", Quantity: "1", UnitPrice: "1.00"},
		}); err != nil {
			t.Fatalf("seed invoice: %v", err)
		}
	}

	before2 := tracer.count.Load()
	invoices2, err := invoicing.ListInvoicesWithLines(ctx, tracedPool, firmID2, userID2)
	if err != nil {
		t.Fatalf("ListInvoicesWithLines (20 invoices): %v", err)
	}
	if len(invoices2) != 20 {
		t.Fatalf("expected 20 invoices, got %d", len(invoices2))
	}
	queriesForTwentyInvoices := tracer.count.Load() - before2

	if queriesForFiveInvoices != queriesForTwentyInvoices {
		t.Fatalf("round trips scaled with invoice count: 5 invoices took %d queries, 20 invoices took %d - not a fixed number of round trips",
			queriesForFiveInvoices, queriesForTwentyInvoices)
	}
}
