// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package timetracking

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// PersonTotal is one person's total logged hours over a summary window.
type PersonTotal struct {
	PersonID   uuid.UUID
	TotalHours string
}

// SummaryOptions filters Summary - mirrors ListEntriesOptions.
type SummaryOptions struct {
	PersonID uuid.UUID // zero value means every person
	From, To string    // "YYYY-MM-DD", empty means unbounded
}

// Summary returns total hours per person over opts' window - the
// GET .../time-entries/summary?person_id=&from=&to= endpoint's data
// source. Member-gated read, same tier as ListEntries. Summed by
// Postgres's own exact `numeric` arithmetic, never a Go float - same
// discipline internal/accounting's own reports.go follows.
func Summary(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts SummaryOptions) ([]PersonTotal, error) {
	var totals []PersonTotal
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var personFilter *uuid.UUID
		if opts.PersonID != uuid.Nil {
			personFilter = &opts.PersonID
		}
		rows, err := tx.Query(ctx, `
			SELECT person_id, SUM(hours)::text
			FROM time_entries
			WHERE firm_id = $1
			  AND ($2::uuid IS NULL OR person_id = $2)
			  AND ($3 = '' OR date >= $3::date)
			  AND ($4 = '' OR date <= $4::date)
			GROUP BY person_id
			ORDER BY person_id
		`, firmID, personFilter, opts.From, opts.To)
		if err != nil {
			return fmt.Errorf("summarize time entries: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var t PersonTotal
			if err := rows.Scan(&t.PersonID, &t.TotalHours); err != nil {
				return err
			}
			totals = append(totals, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return totals, nil
}
