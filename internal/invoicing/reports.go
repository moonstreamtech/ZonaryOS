// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package invoicing

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// AgingBucket is one row of the receivables aging report - every unpaid
// invoice whose age (today minus due_date, or issued_date when due_date
// is unset - see ReceivablesAging's own SQL) falls in [MinDaysOverdue,
// MaxDaysOverdue) rolls into exactly one of these four buckets.
type AgingBucket struct {
	// Label is one of "0-30", "31-60", "61-90", "90+" - a fixed,
	// hand-defined set (the design brief's own bucket boundaries), not a
	// parametric range a firm configures.
	Label string
	Count int
	// Outstanding is SUM(total) across every invoice in this bucket,
	// formatted as a decimal string - computed by Postgres's own exact
	// `numeric` arithmetic, never a Go float, same discipline every other
	// money aggregate in this codebase already follows.
	Outstanding string
}

// ReceivablesAging groups firmID's unpaid invoices (status 'sent' or
// 'overdue' - 'draft' isn't yet a real receivable, 'paid'/'cancelled'
// are no longer outstanding at all) by how many days overdue they are,
// using due_date when set and issued_date otherwise (an invoice with no
// due_date still has an age, reckoned from when it was issued - the same
// "don't silently drop rows with a NULL optional field" reasoning
// internal/reports' own KPI queries already apply via COALESCE/LEFT
// JOIN). Age is computed entirely in SQL (CURRENT_DATE - COALESCE(due_date,
// issued_date)), never in Go, so it's always "as of right now" rather
// than a value that could go stale between the query and the response
// being formatted. Member-gated: reading an aggregate report is ordinary
// firm data visibility, the same tier as internal/reports.GetDashboardKPIs.
func ReceivablesAging(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]AgingBucket, error) {
	var buckets []AgingBucket
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			WITH aged AS (
				SELECT
					total,
					(CURRENT_DATE - COALESCE(due_date, issued_date)) AS days_overdue
				FROM invoices
				WHERE status IN ('sent', 'overdue')
			),
			bucketed AS (
				SELECT
					CASE
						WHEN days_overdue <= 30 THEN '0-30'
						WHEN days_overdue <= 60 THEN '31-60'
						WHEN days_overdue <= 90 THEN '61-90'
						ELSE '90+'
					END AS label,
					total
				FROM aged
			)
			SELECT label, count(*), COALESCE(SUM(total), 0)::text
			FROM bucketed
			GROUP BY label
		`)
		if err != nil {
			return fmt.Errorf("aging report: %w", err)
		}
		defer rows.Close()

		byLabel := map[string]AgingBucket{}
		for rows.Next() {
			var b AgingBucket
			if err := rows.Scan(&b.Label, &b.Count, &b.Outstanding); err != nil {
				return err
			}
			byLabel[b.Label] = b
		}
		if err := rows.Err(); err != nil {
			return err
		}

		// Every one of the four fixed labels is always present in the
		// response, count=0/outstanding="0.0000" if nothing fell into it -
		// the same "every KPI key always appears, zero when empty"
		// reasoning internal/reports.computeKPI's own callers rely on, so
		// a frontend table never has to guess which buckets might be
		// missing.
		for _, label := range []string{"0-30", "31-60", "61-90", "90+"} {
			if b, ok := byLabel[label]; ok {
				buckets = append(buckets, b)
			} else {
				buckets = append(buckets, AgingBucket{Label: label, Count: 0, Outstanding: "0.0000"})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return buckets, nil
}
