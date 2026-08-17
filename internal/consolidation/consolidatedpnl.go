// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package consolidation

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
)

// ConsolidatedPnLFirmLine is one member firm's own revenue/expenses/net
// income contribution to the group's consolidated P&L.
type ConsolidatedPnLFirmLine struct {
	FirmID    uuid.UUID
	FirmName  string
	Revenue   string
	Expenses  string
	NetIncome string
}

// ConsolidatedPnLReport is a group's combined P&L for [From, To]: each
// member firm's own totals, plus the group-wide rollup. Deliberately NOT
// broken down per-account across firms: different member firms' charts
// of accounts are independent (Never-Violate Rule 3 - each firm's
// accounts are its own RLS-scoped data), so two firms sharing the same
// account code is coincidence, not a signal they should be summed
// together into one combined account line - see this batch's own scope
// boundary ("keep it minimal ... not a full consolidation engine",
// which a GL-code-standardization/merge step would be well beyond).
// Per-firm totals plus a group rollup avoids that ambiguity while still
// answering "what does the group make as a whole."
type ConsolidatedPnLReport struct {
	GroupID       uuid.UUID
	From          *time.Time
	To            *time.Time
	Firms         []ConsolidatedPnLFirmLine
	TotalRevenue  string
	TotalExpenses string
	NetIncome     string
}

type memberFirm struct {
	FirmID uuid.UUID
	Name   string
}

func groupMemberFirmsWithNames(ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID) ([]memberFirm, error) {
	rows, err := pool.Query(ctx, `
		SELECT f.id, f.name FROM firm_group_members m JOIN firms f ON f.id = m.member_firm_id
		WHERE m.group_id = $1 ORDER BY f.name
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("list group member firms: %w", err)
	}
	defer rows.Close()
	var firms []memberFirm
	for rows.Next() {
		var f memberFirm
		if err := rows.Scan(&f.FirmID, &f.Name); err != nil {
			return nil, err
		}
		firms = append(firms, f)
	}
	return firms, rows.Err()
}

// firmPnLTotalsTx sums firmID's revenue/expense journal_lines for
// [from, to] (either may be nil, same convention
// internal/accounting.reportAccountRows uses), EXCLUDING any
// journal_entries row that is either leg of an intercompany transfer
// (the NOT EXISTS below, matched against intercompany_transactions'
// from_journal_entry_id/to_journal_entry_id) - Part 3's own "eliminating
// inter-company transactions to avoid double-counting" requirement.
// Tied to real intercompany_transactions rows rather than a
// source_type string match, so the elimination stays correct even if a
// future caller reuses "intercompany_transfer" as a source_type for
// something PostIntercompanyTransfer didn't itself post.
//
// ciaudit:ignore-firmid-check: called only from GetConsolidatedPnL,
// which has already run its own allowlist check (platform-admin, not
// firm membership - the caller is ZonaryOS staff, not a member of
// firmID) before ever reaching this - the same reasoning
// internal/platformadmin.firmCountsAndAuditTx's own suppression already
// documents for the identical per-firm-scoped-loop shape.
func firmPnLTotalsTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, from, to *time.Time) (revenue, expenses string, err error) {
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(CASE WHEN jl.side = 'credit' THEN jl.amount WHEN jl.side = 'debit' THEN -jl.amount ELSE 0 END), 0)::numeric(30,4)::text
		FROM journal_lines jl
		JOIN journal_entries je ON je.id = jl.entry_id
		JOIN accounts a ON a.id = jl.account_id
		WHERE a.firm_id = $1 AND a.type = 'revenue'
			AND NOT EXISTS (
				SELECT 1 FROM intercompany_transactions ic
				WHERE ic.from_journal_entry_id = je.id OR ic.to_journal_entry_id = je.id
			)
			AND ($2::timestamptz IS NULL OR je.posted_at >= $2)
			AND ($3::timestamptz IS NULL OR je.posted_at <= $3)
	`, firmID, from, to).Scan(&revenue); err != nil {
		return "", "", fmt.Errorf("sum revenue: %w", err)
	}

	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(CASE WHEN jl.side = 'debit' THEN jl.amount WHEN jl.side = 'credit' THEN -jl.amount ELSE 0 END), 0)::numeric(30,4)::text
		FROM journal_lines jl
		JOIN journal_entries je ON je.id = jl.entry_id
		JOIN accounts a ON a.id = jl.account_id
		WHERE a.firm_id = $1 AND a.type = 'expense'
			AND NOT EXISTS (
				SELECT 1 FROM intercompany_transactions ic
				WHERE ic.from_journal_entry_id = je.id OR ic.to_journal_entry_id = je.id
			)
			AND ($2::timestamptz IS NULL OR je.posted_at >= $2)
			AND ($3::timestamptz IS NULL OR je.posted_at <= $3)
	`, firmID, from, to).Scan(&expenses); err != nil {
		return "", "", fmt.Errorf("sum expenses: %w", err)
	}
	return revenue, expenses, nil
}

// sumDecimalsPool/diffDecimalsPool add/subtract two decimal strings via
// Postgres's own exact `numeric` arithmetic (never Go float math, the
// same discipline internal/accounting.sumDecimals/diffDecimals already
// establish) - the pool-level (no transaction/RLS context needed)
// variant, since combining two already-computed totals touches no
// tenant-scoped table.
func sumDecimalsPool(ctx context.Context, pool *pgxpool.Pool, a, b string) (string, error) {
	var result string
	if err := pool.QueryRow(ctx, `SELECT ($1::numeric + $2::numeric)::text`, a, b).Scan(&result); err != nil {
		return "", fmt.Errorf("sum decimals: %w", err)
	}
	return result, nil
}

func diffDecimalsPool(ctx context.Context, pool *pgxpool.Pool, a, b string) (string, error) {
	var result string
	if err := pool.QueryRow(ctx, `SELECT ($1::numeric - $2::numeric)::text`, a, b).Scan(&result); err != nil {
		return "", fmt.Errorf("diff decimals: %w", err)
	}
	return result, nil
}

// GetConsolidatedPnL computes groupID's combined P&L for [from, to],
// looping WithFirmContext once per member firm (the same "sanctioned
// per-firm-scoped-loop pattern" internal/platformadmin.ListFirms already
// establishes for a platform-admin caller who is not a member of any of
// the firms being read) - RLS makes a single cross-firm SELECT
// impossible by construction (only one firm's app.current_firm_id can be
// active per query), so summing across firms happens firm-by-firm in Go,
// with each individual sum still computed by Postgres's own exact
// `numeric` arithmetic. Platform-admin only.
func GetConsolidatedPnL(ctx context.Context, pool *pgxpool.Pool, allow platformadmin.Allowlist, caller identity.Identity, groupID uuid.UUID, from, to *time.Time) (ConsolidatedPnLReport, error) {
	if !allow.Contains(caller.Email) {
		return ConsolidatedPnLReport{}, platformadmin.ErrNotPlatformAdmin
	}

	memberFirms, err := groupMemberFirmsWithNames(ctx, pool, groupID)
	if err != nil {
		return ConsolidatedPnLReport{}, err
	}
	var groupExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM firm_groups WHERE id = $1)`, groupID).Scan(&groupExists); err != nil {
		return ConsolidatedPnLReport{}, fmt.Errorf("check firm group exists: %w", err)
	}
	if !groupExists {
		return ConsolidatedPnLReport{}, ErrGroupNotFound
	}

	report := ConsolidatedPnLReport{GroupID: groupID, From: from, To: to, TotalRevenue: "0", TotalExpenses: "0"}
	for _, f := range memberFirms {
		var revenue, expenses string
		err := zdb.WithFirmContext(ctx, pool, f.FirmID, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			revenue, expenses, err = firmPnLTotalsTx(ctx, tx, f.FirmID, from, to)
			return err
		})
		if err != nil {
			return ConsolidatedPnLReport{}, fmt.Errorf("compute P&L for firm %s: %w", f.FirmID, err)
		}

		net, err := diffDecimalsPool(ctx, pool, revenue, expenses)
		if err != nil {
			return ConsolidatedPnLReport{}, err
		}
		report.Firms = append(report.Firms, ConsolidatedPnLFirmLine{
			FirmID: f.FirmID, FirmName: f.Name, Revenue: revenue, Expenses: expenses, NetIncome: net,
		})

		if report.TotalRevenue, err = sumDecimalsPool(ctx, pool, report.TotalRevenue, revenue); err != nil {
			return ConsolidatedPnLReport{}, err
		}
		if report.TotalExpenses, err = sumDecimalsPool(ctx, pool, report.TotalExpenses, expenses); err != nil {
			return ConsolidatedPnLReport{}, err
		}
	}

	if report.NetIncome, err = diffDecimalsPool(ctx, pool, report.TotalRevenue, report.TotalExpenses); err != nil {
		return ConsolidatedPnLReport{}, err
	}
	return report, nil
}
