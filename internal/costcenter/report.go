// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package costcenter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// CostCenterPnLLine is one cost center's revenue/expense/net contribution
// for a period - CostCenterID is nil for journal_lines with no
// cost_center_id set at all ("Unassigned"), the honest bucket for every
// line posted before this batch existed or by a bridge whose payload
// never carried a cost_center_id field.
type CostCenterPnLLine struct {
	CostCenterID *uuid.UUID
	Name         string
	Revenue      string
	Expenses     string
	Net          string
}

// GetCostCenterPnL computes firmID's P&L grouped by cost center instead
// of by account - GET .../reports/cost-center-pnl?from=&to=. Revenue
// lines (accounts.type = 'revenue') are summed credit-minus-debit,
// expense lines (accounts.type = 'expense') debit-minus-credit - the
// exact same sign convention internal/accounting.reportAccountRows uses
// for the flat P&L, just grouped by jl.cost_center_id instead of by
// account. Member-gated (not owner-gated), same tier as
// internal/accounting.GetProfitAndLoss - reading a financial report is
// ordinary firm data visibility.
//
// This package queries journal_lines/journal_entries/accounts directly
// (raw SQL) rather than importing internal/accounting - the same "a
// report package reads another module's tables directly, without a Go
// import, when it only needs read access and the other module's own
// authorization has already run at this call's own entry point" pattern
// internal/reports' KPI computations already establish for
// internal/asset/internal/warehouse/etc.
func GetCostCenterPnL(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, from, to *time.Time) ([]CostCenterPnLLine, error) {
	var lines []CostCenterPnLLine
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT
				cc.id, COALESCE(cc.name, 'Unassigned'),
				COALESCE(SUM(CASE WHEN a.type = 'revenue' AND jl.side = 'credit' THEN jl.amount
					WHEN a.type = 'revenue' AND jl.side = 'debit' THEN -jl.amount ELSE 0 END), 0)::numeric(30,4)::text,
				COALESCE(SUM(CASE WHEN a.type = 'expense' AND jl.side = 'debit' THEN jl.amount
					WHEN a.type = 'expense' AND jl.side = 'credit' THEN -jl.amount ELSE 0 END), 0)::numeric(30,4)::text
			FROM journal_lines jl
			JOIN journal_entries je ON je.id = jl.entry_id
			JOIN accounts a ON a.id = jl.account_id
			LEFT JOIN cost_centers cc ON cc.id = jl.cost_center_id
			WHERE jl.firm_id = $1 AND a.type IN ('revenue', 'expense')
				AND ($2::timestamptz IS NULL OR je.posted_at >= $2)
				AND ($3::timestamptz IS NULL OR je.posted_at <= $3)
			GROUP BY cc.id, cc.name
			ORDER BY cc.name NULLS FIRST
		`, firmID, from, to)
		if err != nil {
			return fmt.Errorf("query cost center P&L: %w", err)
		}
		for rows.Next() {
			var l CostCenterPnLLine
			if err := rows.Scan(&l.CostCenterID, &l.Name, &l.Revenue, &l.Expenses); err != nil {
				rows.Close()
				return err
			}
			lines = append(lines, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		// Net is computed per row only after the row-producing query is
		// fully closed - pgx's default simple connection can't have a
		// second query in flight on the same tx while an earlier Rows is
		// still open ("conn busy").
		for i := range lines {
			if err := tx.QueryRow(ctx, `SELECT ($1::numeric - $2::numeric)::text`, lines[i].Revenue, lines[i].Expenses).Scan(&lines[i].Net); err != nil {
				return fmt.Errorf("compute cost center net: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lines, nil
}
