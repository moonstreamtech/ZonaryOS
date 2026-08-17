// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Financial reports (Vision §3's natural next step once the ledger core
// exists): Profit & Loss and Balance Sheet, both computed views over
// journal_entries/journal_lines - no new tables, no stored report rows.
// Every total here is computed by Postgres's own exact `numeric`
// arithmetic (never a Go float), the same discipline PostJournalEntryTx's
// own balance check already established (journal.go).
package accounting

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

// ReportLine is one account's contribution to a report - a real chart-of-
// accounts row plus a computed amount, not a synthetic bucket, so a
// reader can trace every reported figure back to its account.
// BudgetedAmount/Variance/VariancePercent (budget management/cost
// centers/financial planning batch) are populated by GetProfitAndLoss
// only - nil for every other report built from this same struct
// (GetBalanceSheet) and nil even on a PnL line when no 'active' budget
// covers the requested [from,to] period, or that budget has no line for
// this account. Variance = Amount - BudgetedAmount (positive means over
// budget for an expense line, under for a revenue line - the reader's
// own account type tells them which); VariancePercent is Variance as a
// percentage of BudgetedAmount, nil (not divide-by-zero) when
// BudgetedAmount is exactly zero.
type ReportLine struct {
	AccountID       uuid.UUID
	Code            string
	Name            string
	Amount          string
	BudgetedAmount  *string
	Variance        *string
	VariancePercent *string
}

// reportAccountRows runs the shared "per-account signed sum over a date-
// filtered set of journal lines" query both PnL (revenue/expense) and
// BalanceSheet (asset/liability/equity) sections are built from.
// creditPositive controls the sign convention: true sums credits minus
// debits (revenue/liability/equity's normal balance side), false sums
// debits minus credits (asset/expense's normal balance side) - mirrors
// AccountType.normalDebitBalance's own convention in accounts.go, just
// parameterized instead of branching per account type there. from/to
// bound journal_entries.posted_at inclusively; either may be nil to leave
// that bound unset (matching internal/auditlog.List's own from/to
// convention). Returns the per-account lines (ordered by code, an empty
// slice - not an error - for a firm with no accounts of the given type
// yet) and the total across all of them, both computed by Postgres, never
// a Go-side sum over the returned strings.
//
// sign is a fixed Go string literal ("1"/"-1" below), never caller/
// request-supplied data - safe to concatenate directly into the query
// text (there is no SQL injection surface here, unlike a value that ever
// originated from an HTTP request).
//
// ciaudit:ignore-firmid-check: internal helper called only by
// GetProfitAndLoss/GetBalanceSheet, both of which already run
// permission.IsMember before reaching this call - firmID here scopes the
// query (defense in depth alongside RLS, the same reasoning
// internal/workflow's checkReferenceField doc comment uses for its own
// suppression), it is not itself an authorization decision.
func reportAccountRows(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, accountType AccountType, creditPositive bool, from, to *time.Time) ([]ReportLine, string, error) {
	sign := "-1"
	if creditPositive {
		sign = "1"
	}
	rows, err := tx.Query(ctx, `
		SELECT id, code, name, amount::text, COALESCE((SUM(amount) OVER())::text, '0')
		FROM (
			SELECT a.id, a.code, a.name,
				`+sign+` * (
					COALESCE(SUM(CASE WHEN jl.side = 'credit'
						AND ($3::timestamptz IS NULL OR je.posted_at >= $3)
						AND ($4::timestamptz IS NULL OR je.posted_at <= $4)
						THEN jl.amount ELSE 0 END), 0) -
					COALESCE(SUM(CASE WHEN jl.side = 'debit'
						AND ($3::timestamptz IS NULL OR je.posted_at >= $3)
						AND ($4::timestamptz IS NULL OR je.posted_at <= $4)
						THEN jl.amount ELSE 0 END), 0)
				) AS amount
			FROM accounts a
			LEFT JOIN journal_lines jl ON jl.account_id = a.id
			LEFT JOIN journal_entries je ON je.id = jl.entry_id
			WHERE a.firm_id = $1 AND a.type = $2
			GROUP BY a.id, a.code, a.name
		) sub
		ORDER BY code
	`, firmID, string(accountType), from, to)
	if err != nil {
		return nil, "", fmt.Errorf("query %s accounts: %w", accountType, err)
	}
	defer rows.Close()

	var lines []ReportLine
	total := "0"
	for rows.Next() {
		var l ReportLine
		if err := rows.Scan(&l.AccountID, &l.Code, &l.Name, &l.Amount, &total); err != nil {
			return nil, "", err
		}
		lines = append(lines, l)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	return lines, total, nil
}

// attachBudgetVarianceTx populates lines' BudgetedAmount/Variance/
// VariancePercent in place from firmID's single 'active' budget covering
// [from,to] (if any) - a no-op (every line's budget fields stay nil)
// when from or to is nil (no period to match a budget's own
// period_start/period_end against), or when no 'active' budget covers
// that period, or a matched budget simply has no line for a given
// account. "Only one active budget per period" (internal/budget's own
// enforcement, not re-checked here) is what makes "the" active budget
// well-defined; this query would silently pick whichever one budgets.id
// sorts first if that invariant were ever violated, the same
// defense-in-depth-only posture every other cross-package invariant in
// this codebase takes (the enforcement lives at the write path, not
// re-verified at every read).
//
// This queries budgets/budget_lines directly (raw SQL), not via
// internal/budget's own Go API - internal/accounting stays a leaf
// dependency (see journal.go's own doc comment on LineInput.CostCenterID
// for the identical reasoning), the same "a report reads another
// module's tables directly once its own caller has already authorized
// the read" pattern internal/reports' KPI computations and
// internal/costcenter.GetCostCenterPnL both already establish.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// GetProfitAndLoss, which has already run permission.IsMember before
// reaching this call - firmID here scopes the query (defense in depth
// alongside RLS), it is not itself an authorization decision.
func attachBudgetVarianceTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, lines []ReportLine, from, to *time.Time) error {
	if from == nil || to == nil || len(lines) == 0 {
		return nil
	}

	var budgetID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM budgets
		WHERE firm_id = $1 AND status = 'active' AND period_start <= $3::date AND period_end >= $2::date
		ORDER BY period_start LIMIT 1
	`, firmID, from, to).Scan(&budgetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up active budget for period: %w", err)
	}

	planned := make(map[uuid.UUID]string, len(lines))
	rows, err := tx.Query(ctx, `SELECT account_id, planned_amount::text FROM budget_lines WHERE firm_id = $1 AND budget_id = $2`, firmID, budgetID)
	if err != nil {
		return fmt.Errorf("list budget lines for variance: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var accountID uuid.UUID
		var amount string
		if err := rows.Scan(&accountID, &amount); err != nil {
			return err
		}
		planned[accountID] = amount
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range lines {
		budgeted, ok := planned[lines[i].AccountID]
		if !ok {
			continue
		}
		var variance string
		var variancePercent *string
		if err := tx.QueryRow(ctx, `SELECT ($1::numeric - $2::numeric)::text`, lines[i].Amount, budgeted).Scan(&variance); err != nil {
			return fmt.Errorf("compute budget variance: %w", err)
		}
		if err := tx.QueryRow(ctx, `
			SELECT CASE WHEN $2::numeric = 0 THEN NULL ELSE (($1::numeric - $2::numeric) / $2::numeric * 100)::text END
		`, lines[i].Amount, budgeted).Scan(&variancePercent); err != nil {
			return fmt.Errorf("compute budget variance percent: %w", err)
		}
		lines[i].BudgetedAmount = &budgeted
		lines[i].Variance = &variance
		lines[i].VariancePercent = variancePercent
	}
	return nil
}

// sumDecimals adds a and b using Postgres's own exact `numeric`
// arithmetic - the same "never a Go float, never hand-rolled decimal
// math" discipline as isZeroDecimal/PostJournalEntryTx's own balance
// check (journal.go), applied here to combine two already-computed
// totals (e.g. total revenue and total expenses) into a third.
func sumDecimals(ctx context.Context, tx pgx.Tx, a, b string) (string, error) {
	var result string
	if err := tx.QueryRow(ctx, `SELECT ($1::numeric + $2::numeric)::text`, a, b).Scan(&result); err != nil {
		return "", fmt.Errorf("sum decimals: %w", err)
	}
	return result, nil
}

// diffDecimals subtracts b from a, same reasoning as sumDecimals.
func diffDecimals(ctx context.Context, tx pgx.Tx, a, b string) (string, error) {
	var result string
	if err := tx.QueryRow(ctx, `SELECT ($1::numeric - $2::numeric)::text`, a, b).Scan(&result); err != nil {
		return "", fmt.Errorf("diff decimals: %w", err)
	}
	return result, nil
}

// PnLReport is a Profit & Loss statement for one period: every revenue
// and expense account's own contribution (not just a single total), plus
// the rolled-up totals and net income - the design brief's "grouped by
// account, not just a single total, so the frontend can render a real
// P&L statement."
type PnLReport struct {
	From          *time.Time
	To            *time.Time
	Revenue       []ReportLine
	Expenses      []ReportLine
	TotalRevenue  string
	TotalExpenses string
	NetIncome     string
}

// GetProfitAndLoss computes firmID's P&L for [from, to] (either bound may
// be nil - see reportAccountRows' doc comment). Revenue accounts are
// summed credit-minus-debit (their normal balance side); expense accounts
// debit-minus-credit - the design brief's exact formula. Member-gated
// (not owner-gated): reading a financial report is ordinary firm data
// visibility, the same tier as ListJournalEntries/ListAccounts - only
// chart-of-accounts management and manual entries are owner-only.
func GetProfitAndLoss(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, from, to *time.Time) (PnLReport, error) {
	var report PnLReport
	report.From, report.To = from, to

	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		report.Revenue, report.TotalRevenue, err = reportAccountRows(ctx, tx, firmID, AccountTypeRevenue, true, from, to)
		if err != nil {
			return err
		}
		report.Expenses, report.TotalExpenses, err = reportAccountRows(ctx, tx, firmID, AccountTypeExpense, false, from, to)
		if err != nil {
			return err
		}
		report.NetIncome, err = diffDecimals(ctx, tx, report.TotalRevenue, report.TotalExpenses)
		if err != nil {
			return err
		}

		if err := attachBudgetVarianceTx(ctx, tx, firmID, report.Revenue, from, to); err != nil {
			return err
		}
		return attachBudgetVarianceTx(ctx, tx, firmID, report.Expenses, from, to)
	})
	if err != nil {
		return PnLReport{}, err
	}
	return report, nil
}

// BalanceSheetReport is a Balance Sheet as of one instant: every asset,
// liability, and equity account's own balance up to asOf, plus rolled-up
// totals. CurrentEarnings is net income accrued since the last closing
// entry (there is no period-closing mechanism in this batch - see this
// package's scope boundary), folded into TotalEquity as its own line -
// see GetBalanceSheet's doc comment for why this is the correct
// accounting treatment, not a shortcut. Balanced/Difference is the
// literal Assets = Liabilities + Equity check (Never-Violate-adjacent
// data-integrity signal, not merely a cosmetic figure) - see GetBalanceSheet.
type BalanceSheetReport struct {
	AsOf             time.Time
	Assets           []ReportLine
	Liabilities      []ReportLine
	Equity           []ReportLine
	CurrentEarnings  string
	TotalAssets      string
	TotalLiabilities string
	// TotalEquity is the sum of Equity's own accounts PLUS CurrentEarnings
	// - see this type's own doc comment.
	TotalEquity string
	Balanced    bool
	Difference  string
}

// GetBalanceSheet computes firmID's Balance Sheet as of asOf. Asset
// accounts are summed debit-minus-credit; liability and equity accounts
// credit-minus-debit - all up to and including asOf - the design brief's
// exact formula.
//
// CurrentEarnings (design decision, documented per the task's own
// instruction to explain accounting-treatment choices): this batch has
// no period-closing mechanism - revenue and expense accounts are never
// automatically zeroed into retained earnings at a fiscal year-end (that
// would need a real "close the books" concept, out of scope here, see
// this package's scope boundary). Under real double-entry bookkeeping,
// Assets = Liabilities + Equity holds ONLY once each period's net income
// has been closed into equity; without a closing step, any firm with
// unclosed revenue/expense (e.g. every firm the moment it records its
// first sale - stock_to_sale's own record_sale posts DR Trade
// Receivables / CR Sales Revenue, which is an asset increasing with
// nothing on the liability/equity side) would show as permanently,
// structurally "out of balance" - not a bug, but a false, permanent
// alarm that makes the Balanced flag meaningless. The standard interim/
// unaudited-books treatment (and the correct one here) is to report
// accrued-but-unclosed net income as a "Current Earnings" component of
// equity - exactly what a real accounting system's interim balance sheet
// shows before year-end closing entries are posted. With that folded in,
// Balanced staying true under normal operation is the expected, correct
// state; Balanced=false is then a genuine data-integrity signal (e.g. an
// entry that only touched asset/liability/equity accounts but somehow
// still doesn't balance would indicate a bug in PostJournalEntryTx's own
// invariant, not an artifact of missing closing entries), retained as a
// literal computed-not-assumed check per the design brief, not removed
// just because it should normally read true.
func GetBalanceSheet(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, asOf time.Time) (BalanceSheetReport, error) {
	var report BalanceSheetReport
	report.AsOf = asOf

	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		report.Assets, report.TotalAssets, err = reportAccountRows(ctx, tx, firmID, AccountTypeAsset, false, nil, &asOf)
		if err != nil {
			return err
		}
		report.Liabilities, report.TotalLiabilities, err = reportAccountRows(ctx, tx, firmID, AccountTypeLiability, true, nil, &asOf)
		if err != nil {
			return err
		}
		report.Equity, report.TotalEquity, err = reportAccountRows(ctx, tx, firmID, AccountTypeEquity, true, nil, &asOf)
		if err != nil {
			return err
		}

		revenue, totalRevenue, err := reportAccountRows(ctx, tx, firmID, AccountTypeRevenue, true, nil, &asOf)
		if err != nil {
			return err
		}
		_ = revenue
		expenses, totalExpenses, err := reportAccountRows(ctx, tx, firmID, AccountTypeExpense, false, nil, &asOf)
		if err != nil {
			return err
		}
		_ = expenses
		report.CurrentEarnings, err = diffDecimals(ctx, tx, totalRevenue, totalExpenses)
		if err != nil {
			return err
		}

		report.TotalEquity, err = sumDecimals(ctx, tx, report.TotalEquity, report.CurrentEarnings)
		if err != nil {
			return err
		}

		liabilitiesPlusEquity, err := sumDecimals(ctx, tx, report.TotalLiabilities, report.TotalEquity)
		if err != nil {
			return err
		}
		report.Difference, err = diffDecimals(ctx, tx, report.TotalAssets, liabilitiesPlusEquity)
		if err != nil {
			return err
		}
		report.Balanced = isZeroDecimal(report.Difference)
		return nil
	})
	if err != nil {
		return BalanceSheetReport{}, err
	}
	return report, nil
}
