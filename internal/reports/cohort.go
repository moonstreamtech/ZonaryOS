// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Part 3 of the analytics/BI/advanced reporting batch: cohort analysis.
// Only ONE (entity, cohort_by, metric) combination is implemented -
// customers cohorted by their own created_at month, with invoice_total
// as the per-period metric - the exact shape this batch's own design
// brief asks for ("classic SaaS retention metric"). A request for any
// other combination returns ErrCohortSpecNotSupported (a 400), never a
// silent no-op or a made-up result: this is a deliberately narrow first
// cohort report, not a general cohort-analysis query language - a future
// batch that needs a second (entity, cohort_by, metric) combination adds
// its own case, the same "narrow now, extend later" posture
// internal/integration/transform.go's own applyOne takes for new
// transforms.
package reports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// ErrCohortSpecNotSupported means the requested (entity, cohortBy,
// metric) combination isn't the one this batch implements - see this
// file's own doc comment.
var ErrCohortSpecNotSupported = errors.New("cohort spec not supported")

const (
	cohortEntityCustomers  = "customers"
	cohortByCreatedAt      = "created_at"
	cohortMetricInvoiceTot = "invoice_total"
)

// defaultCohortPeriods/maxCohortPeriods bound the ?periods= param - a
// cohort table with an unbounded number of period columns would be both
// a slow query (one more month of invoice data to scan per period) and
// an unusable UI (nobody reads a 500-column table).
const (
	defaultCohortPeriods = 6
	maxCohortPeriods     = 24
)

// CohortSpec is GET .../reports/cohorts' own query-param shape.
type CohortSpec struct {
	Entity   string
	CohortBy string
	Metric   string
	Periods  int
}

// CohortTable is CohortAnalysis' own result: Cohorts, oldest first, each
// with Periods+1 Values (period 0 = the cohort's own creation month,
// period 1 = one month later, ...) - a 2D array (cohorts x periods),
// exactly as this batch's own design brief asks for.
type CohortTable struct {
	Cohorts []CohortRow
	Periods int
}

// CohortRow is one cohort (e.g. "customers created in 2026-01") and its
// own per-period metric values.
type CohortRow struct {
	CohortMonth time.Time
	CohortSize  int
	Values      []*float64 // nil at a period index with no data yet (not 0 - a real, distinct "no data" vs. "zero revenue" signal)
}

// CohortAnalysis computes CohortTable for spec - member-gated, same tier
// as RunReport (reading data the caller can already see, no structural
// firm decision). See this file's own doc comment for why spec must be
// exactly (customers, created_at, invoice_total) today.
func CohortAnalysis(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, spec CohortSpec) (CohortTable, error) {
	if spec.Entity != cohortEntityCustomers || spec.CohortBy != cohortByCreatedAt || spec.Metric != cohortMetricInvoiceTot {
		return CohortTable{}, fmt.Errorf("%w: entity=%q cohort_by=%q metric=%q (only entity=customers cohort_by=created_at metric=invoice_total is implemented)",
			ErrCohortSpecNotSupported, spec.Entity, spec.CohortBy, spec.Metric)
	}
	periods := spec.Periods
	if periods <= 0 {
		periods = defaultCohortPeriods
	}
	if periods > maxCohortPeriods {
		periods = maxCohortPeriods
	}

	var table CohortTable
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		// customerCohort maps a customer to the first-of-month their own
		// row was created - the cohort key.
		customerCohort := map[uuid.UUID]time.Time{}
		cohortSize := map[time.Time]int{}
		rows, err := tx.Query(ctx, `SELECT id, date_trunc('month', created_at) FROM customers WHERE firm_id = $1`, firmID)
		if err != nil {
			return fmt.Errorf("list customers for cohort: %w", err)
		}
		for rows.Next() {
			var id uuid.UUID
			var month time.Time
			if err := rows.Scan(&id, &month); err != nil {
				rows.Close()
				return err
			}
			customerCohort[id] = month
			cohortSize[month]++
		}
		if err := rows.Err(); err != nil {
			return err
		}

		// values[cohortMonth][periodIndex] accumulates SUM(invoices.total)
		// for every customer in that cohort whose invoice falls exactly
		// periodIndex months after their own cohort month.
		values := map[time.Time]map[int]float64{}
		invRows, err := tx.Query(ctx, `
			SELECT i.customer_id, date_trunc('month', i.issued_date), i.total::float8
			FROM invoices i
			WHERE i.firm_id = $1 AND i.customer_id IS NOT NULL
		`, firmID)
		if err != nil {
			return fmt.Errorf("list invoices for cohort: %w", err)
		}
		defer invRows.Close()
		for invRows.Next() {
			var customerID uuid.UUID
			var invoiceMonth time.Time
			var total float64
			if err := invRows.Scan(&customerID, &invoiceMonth, &total); err != nil {
				return err
			}
			cohortMonth, ok := customerCohort[customerID]
			if !ok {
				continue
			}
			period := monthsBetween(cohortMonth, invoiceMonth)
			if period < 0 || period >= periods {
				continue
			}
			if values[cohortMonth] == nil {
				values[cohortMonth] = map[int]float64{}
			}
			values[cohortMonth][period] += total
		}
		if err := invRows.Err(); err != nil {
			return err
		}

		months := make([]time.Time, 0, len(cohortSize))
		for m := range cohortSize {
			months = append(months, m)
		}
		sortTimes(months)

		rowsOut := make([]CohortRow, 0, len(months))
		for _, m := range months {
			row := CohortRow{CohortMonth: m, CohortSize: cohortSize[m], Values: make([]*float64, periods)}
			for p := 0; p < periods; p++ {
				if v, ok := values[m][p]; ok {
					vCopy := v
					row.Values[p] = &vCopy
				}
			}
			rowsOut = append(rowsOut, row)
		}
		table = CohortTable{Cohorts: rowsOut, Periods: periods}
		return nil
	})
	if err != nil {
		return CohortTable{}, err
	}
	return table, nil
}

// monthsBetween is the integer number of calendar months from a to b
// (both already truncated to the first of their own month) - the
// cohort's own "period index" (0 = same month as the cohort, 1 = one
// month later, ...).
func monthsBetween(a, b time.Time) int {
	return (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
}

func sortTimes(ts []time.Time) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j].Before(ts[j-1]); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}
