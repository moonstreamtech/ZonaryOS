// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package reports is Vision §3's reporting foundation: a KPI dashboard
// over data that already exists in internal/accounting (journal
// entries/accounts) and internal/workflow (instances) and internal/hr
// (people) - no new data, just aggregations, and no new database table
// either. This package is the first genuinely cross-module read (it
// imports all three of the packages above), which is architecturally new
// for this codebase but not risky: it only ever reads, through the same
// RLS-scoped, IsMember-gated path every other read in this system uses.
//
// Each KPI is a small declarative KPIDescriptor - name/kind/what it
// reads - not a bespoke hand-written SQL query per KPI. Adding a KPI that
// fits one of the existing Kinds (a single account balance, an account
// balance compared across two periods, a workflow-instance count in a
// set of states, or a people count by status) is a one-line addition to
// the kpiDescriptors slice below, not a new code path; only a KPI that
// needs a genuinely new *shape* of aggregation needs a new Kind and its
// own compute function.
package reports

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// ErrFirmNotFound means the caller isn't a member of the given firm at
// all - same convention as every other package's ErrFirmNotFound.
var ErrFirmNotFound = errFirmNotFound{}

type errFirmNotFound struct{}

func (errFirmNotFound) Error() string { return "firm not found" }

// KPIKind is the shape of aggregation a KPIDescriptor asks for - a
// closed, small set (see this package's own doc comment for why a new
// KPI usually doesn't need a new one).
type KPIKind string

const (
	// KPIKindAccountBalanceNow: the current balance of SUM(AccountCodes),
	// as of now, on those accounts' normal balance side (NormalDebit).
	KPIKindAccountBalanceNow KPIKind = "account_balance_now"
	// KPIKindAccountBalancePeriod: SUM(AccountCodes)'s balance over one
	// Period ("current_month"/"previous_month") only - not cumulative
	// since account inception, unlike KPIKindAccountBalanceNow. Used to
	// compare two periods (e.g. "revenue this month" and "revenue last
	// month" are two separate descriptors of this Kind, one per Period).
	KPIKindAccountBalancePeriod KPIKind = "account_balance_period"
	// KPIKindWorkflowStateCount: how many WorkflowKey instances currently
	// sit in any of States.
	KPIKindWorkflowStateCount KPIKind = "workflow_state_count"
	// KPIKindPersonCount: how many people rows have PersonStatus.
	KPIKindPersonCount KPIKind = "person_count"
)

// Period is KPIKindAccountBalancePeriod's own descriptor field - which
// calendar period (relative to now, computed at query time) to sum over.
type Period string

const (
	PeriodCurrentMonth  Period = "current_month"
	PeriodPreviousMonth Period = "previous_month"
)

// KPIDescriptor is one dashboard tile's definition - a name (a stable
// key, translated client-side per Never-Violate Rule 4, never literal
// UI text returned from the backend) plus whatever a KPIKind needs to
// compute it.
type KPIDescriptor struct {
	// Key is a stable, machine-readable identifier (e.g.
	// "revenueThisMonth") - the frontend looks this up in its own i18n
	// catalog for the tile's label, exactly the same "backend returns a
	// key, frontend owns the copy" convention internal/workflow's own
	// state/action keys use for anything genuinely fixed-vocabulary
	// (as opposed to state/action *names*, which are firm-authored data,
	// not app chrome).
	Key  string
	Kind KPIKind

	// AccountCodes/NormalDebit are used by KPIKindAccountBalanceNow/
	// KPIKindAccountBalancePeriod - the chart-of-accounts codes to sum,
	// and which side (debit or credit) is that account type's normal
	// increasing side (see internal/accounting.AccountType.normalDebitBalance's
	// identical convention).
	AccountCodes []string
	NormalDebit  bool
	// Period is used by KPIKindAccountBalancePeriod only.
	Period Period

	// WorkflowKey/States are used by KPIKindWorkflowStateCount.
	WorkflowKey string
	States      []string

	// PersonStatus is used by KPIKindPersonCount.
	PersonStatus string
}

// kpiDescriptors is the dashboard's actual KPI list - the design brief's
// five: revenue this month, revenue last month (so the frontend can show
// the delta - see the "Financials" scope of this batch), outstanding
// receivables, inventory value, open tasks, active people. Account codes
// reference internal/accounting's own exported constants (the same codes
// internal/wizard's chart-of-accounts seeding and stock_to_sale/
// purchase_order's journal templates already use), not duplicated
// literals - a firm's real chart of accounts uses exactly these codes
// whenever the corresponding workflow/module is active.
var kpiDescriptors = []KPIDescriptor{
	{
		Key: "revenueThisMonth", Kind: KPIKindAccountBalancePeriod,
		AccountCodes: []string{accounting.SalesRevenueAccountCode}, NormalDebit: false, Period: PeriodCurrentMonth,
	},
	{
		Key: "revenueLastMonth", Kind: KPIKindAccountBalancePeriod,
		AccountCodes: []string{accounting.SalesRevenueAccountCode}, NormalDebit: false, Period: PeriodPreviousMonth,
	},
	{
		Key: "outstandingReceivables", Kind: KPIKindAccountBalanceNow,
		AccountCodes: []string{accounting.TradeReceivablesAccountCode}, NormalDebit: true,
	},
	{
		Key: "inventoryValue", Kind: KPIKindAccountBalanceNow,
		AccountCodes: []string{accounting.InventoryAccountCode}, NormalDebit: true,
	},
	{
		Key: "openTasks", Kind: KPIKindWorkflowStateCount,
		WorkflowKey: workflow.TaskApprovalKey, States: []string{"open", "in_progress"},
	},
	{
		Key: "activePeople", Kind: KPIKindPersonCount,
		PersonStatus: "active",
	},
}

// KPIResult is one computed dashboard tile - Key ties it back to its
// KPIDescriptor (and the frontend's i18n label), Value is the computed
// figure (a plain decimal or integer string - see each Kind's own
// compute function for which), Unit distinguishes how to render/format
// it client-side.
type KPIResult struct {
	Key   string
	Unit  string // "currency" | "count"
	Value string
}

const (
	unitCurrency = "currency"
	unitCount    = "count"
)

// GetDashboardKPIs computes every descriptor in kpiDescriptors for
// firmID, in order. Member-gated (not owner-gated): reading dashboard
// KPIs is ordinary firm data visibility, the same tier as
// internal/accounting.GetProfitAndLoss/internal/workflow.InstanceCountsByDefinition.
// A single KPI whose underlying data doesn't exist yet for this firm
// (e.g. no Inventory account, no task_approval workflow seeded) reads
// back as a real zero, not an error or a missing tile - see each Kind's
// own compute function for why that's always the correct empty-state
// value here (COALESCE(..., 0) on the SQL side, or a query that simply
// returns 0 rows matched).
func GetDashboardKPIs(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]KPIResult, error) {
	var results []KPIResult
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		for _, d := range kpiDescriptors {
			result, err := computeKPI(ctx, tx, firmID, d)
			if err != nil {
				return fmt.Errorf("compute KPI %q: %w", d.Key, err)
			}
			results = append(results, result)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// computeKPI dispatches on d.Kind - the one place a new Kind (as opposed
// to a new descriptor of an existing Kind) needs a new case.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// GetDashboardKPIs, which already runs permission.IsMember before
// reaching this call - firmID here scopes each query (defense in depth
// alongside RLS), it is not itself an authorization decision.
func computeKPI(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, d KPIDescriptor) (KPIResult, error) {
	switch d.Kind {
	case KPIKindAccountBalanceNow:
		value, err := accountBalanceSum(ctx, tx, firmID, d.AccountCodes, d.NormalDebit, nil, nil)
		if err != nil {
			return KPIResult{}, err
		}
		return KPIResult{Key: d.Key, Unit: unitCurrency, Value: value}, nil

	case KPIKindAccountBalancePeriod:
		from, to := periodBounds(d.Period, time.Now())
		value, err := accountBalanceSum(ctx, tx, firmID, d.AccountCodes, d.NormalDebit, &from, &to)
		if err != nil {
			return KPIResult{}, err
		}
		return KPIResult{Key: d.Key, Unit: unitCurrency, Value: value}, nil

	case KPIKindWorkflowStateCount:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM workflow_instances wi
			JOIN workflow_definitions wd ON wd.id = wi.workflow_definition_id
			JOIN workflow_states ws ON ws.id = wi.current_state_id
			WHERE wi.firm_id = $1 AND wd.key = $2 AND ws.key = ANY($3)
		`, firmID, d.WorkflowKey, d.States).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count workflow instances: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	case KPIKindPersonCount:
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM people WHERE firm_id = $1 AND status = $2`, firmID, d.PersonStatus).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count people: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	default:
		return KPIResult{}, fmt.Errorf("unknown KPI kind %q", d.Kind)
	}
}

// accountBalanceSum sums journal_lines for accounts whose code is in
// codes, on their normalDebit side, optionally bounded to
// [from, to] on journal_entries.posted_at (either nil leaves that bound
// unset - a nil/nil pair sums since account inception, i.e. the current
// balance). COALESCE(..., 0) means an account that doesn't exist yet for
// this firm (e.g. no Inventory account seeded) contributes exactly 0,
// not an error - the honest "this firm has no data for this KPI yet"
// answer, same reasoning GetDashboardKPIs' own doc comment gives.
// Computed via Postgres's own exact `numeric` arithmetic, same
// discipline as internal/accounting's reportAccountRows.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// computeKPI, itself only called by GetDashboardKPIs after
// permission.IsMember has already run - firmID here scopes the query
// (defense in depth alongside RLS), it is not itself an authorization
// decision.
func accountBalanceSum(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, codes []string, normalDebit bool, from, to *time.Time) (string, error) {
	sign := "-1"
	if normalDebit {
		sign = "1"
	}
	var result string
	err := tx.QueryRow(ctx, `
		SELECT ( `+sign+` * (
			COALESCE(SUM(CASE WHEN jl.side = 'debit'
				AND ($3::timestamptz IS NULL OR je.posted_at >= $3)
				AND ($4::timestamptz IS NULL OR je.posted_at <= $4)
				THEN jl.amount ELSE 0 END), 0) -
			COALESCE(SUM(CASE WHEN jl.side = 'credit'
				AND ($3::timestamptz IS NULL OR je.posted_at >= $3)
				AND ($4::timestamptz IS NULL OR je.posted_at <= $4)
				THEN jl.amount ELSE 0 END), 0)
		) )::text
		FROM accounts a
		LEFT JOIN journal_lines jl ON jl.account_id = a.id
		LEFT JOIN journal_entries je ON je.id = jl.entry_id
		WHERE a.firm_id = $1 AND a.code = ANY($2)
	`, firmID, codes, from, to).Scan(&result)
	if err != nil {
		return "", fmt.Errorf("sum account balance: %w", err)
	}
	return result, nil
}

// periodBounds computes [from, to] for a Period, relative to now - the
// first/last instant of the current or previous calendar month, in UTC.
func periodBounds(p Period, now time.Time) (from, to time.Time) {
	now = now.UTC()
	firstOfThisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	switch p {
	case PeriodPreviousMonth:
		firstOfPrevMonth := firstOfThisMonth.AddDate(0, -1, 0)
		return firstOfPrevMonth, firstOfThisMonth.Add(-time.Nanosecond)
	default: // PeriodCurrentMonth
		firstOfNextMonth := firstOfThisMonth.AddDate(0, 1, 0)
		return firstOfThisMonth, firstOfNextMonth.Add(-time.Nanosecond)
	}
}
