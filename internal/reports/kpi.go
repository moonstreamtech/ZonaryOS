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
	"errors"
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
	// KPIKindInventoryKPI (Inventory management batch): dispatches further
	// on InventoryMetric below - neither of this Kind's two metrics
	// (low_stock_products, total_inventory_value) fits any of the four
	// Kinds above: they aggregate products/stock_levels, a shape none of
	// account balances, workflow-state counts, or people counts share -
	// hence one new Kind covering inventory-specific aggregations, rather
	// than forcing either metric into an existing Kind's shape (the design
	// brief's own explicit call here).
	KPIKindInventoryKPI KPIKind = "inventory_kpi"
	// KPIKindCRM (Logistics/CRM batch): dispatches further on CRMMetric
	// below. active_customers (customers converted from the pipeline, not
	// manually created) is a relationship-count over the customers table -
	// a different domain from every existing Kind (accounts, workflow
	// state, people, inventory) and, per this batch's own design brief,
	// deliberately its own Kind rather than folded into KPIKindPersonCount
	// (customers aren't people rows) or KPIKindInventoryKPI (a customer
	// isn't inventory - see this batch's own classification note on
	// pending_deliveries below for the contrasting case that DOES belong
	// under KPIKindInventoryKPI).
	KPIKindCRM KPIKind = "crm"
	// KPIKindReceivables (Invoicing/payment tracking batch): dispatches
	// further on ReceivablesMetric below. Both of this Kind's metrics
	// (overdue_invoices, total_outstanding) read internal/invoicing's own
	// invoices/payments tables directly - a genuinely different data
	// source and shape from KPIKindAccountBalanceNow's own
	// outstandingReceivables descriptor above (which sums the ledger's
	// Trade Receivables account balance). The two intentionally coexist,
	// not a replacement for one another: outstandingReceivables answers
	// "what does the ledger say is owed" (every DR Trade Receivables
	// entry, regardless of whether an invoices row exists behind it -
	// e.g. a manually-posted journal entry), while total_outstanding
	// here answers "what do open INVOICES specifically say is owed,
	// per-invoice, net of their own recorded payments" - the aging
	// report's own per-invoice arithmetic (internal/invoicing.ReceivablesAging),
	// surfaced as a single dashboard number.
	KPIKindReceivables KPIKind = "receivables"
	// KPIKindSalesOrders (sales orders + full procurement cycle batch):
	// dispatches further on SalesOrdersMetric below - both of this Kind's
	// metrics query internal/salesorders' sales_orders table directly
	// (same "own tables live outside this package, queried the same way
	// KPIKindReceivables already queries invoices" reasoning
	// KPIKindReceivables' own doc comment gives), deliberately its own
	// Kind rather than folded into KPIKindReceivables - an order's own
	// fulfillment status (draft/confirmed/picking/shipped/delivered) is a
	// different vocabulary from an invoice's billing status
	// (draft/sent/paid/overdue), so sharing one Kind would mean one
	// metric silently querying the wrong table for the other's status
	// values.
	KPIKindSalesOrders KPIKind = "sales_orders"
	// KPIKindManufacturing (manufacturing module foundation batch):
	// dispatches further on ManufacturingMetric below - queries
	// internal/manufacturing's own production_orders table directly, same
	// "own tables live outside this package, queried the same way
	// KPIKindReceivables/KPIKindSalesOrders already query their own
	// tables" reasoning those Kinds' own doc comments give.
	KPIKindManufacturing KPIKind = "manufacturing"
	// KPIKindOperations (Multi-location CRM/project management batch):
	// dispatches further on OperationsMetric below - active_projects
	// queries internal/project's own projects table, overdue_tasks
	// queries workflow_instances/workflow_states directly (task_approval
	// instances, not a project-specific shape) - the two don't share an
	// owning package the way KPIKindManufacturing/KPIKindSalesOrders do,
	// but both answer "what needs attention operationally right now,"
	// deliberately grouped under one cross-cutting Kind rather than
	// forcing overdue_tasks into KPIKindWorkflowStateCount (which counts
	// instances currently in given States, with no notion of "how long
	// ago" - a genuinely different query shape) or active_projects into
	// a brand new single-metric Kind of its own.
	KPIKindOperations KPIKind = "operations"
	// KPIKindAssets (Asset management/maintenance/facility operations
	// batch): dispatches further on AssetMetric below - both metrics query
	// internal/asset's own assets/maintenance_schedules tables directly,
	// the same "own tables live outside this package, queried the same
	// way" reasoning KPIKindManufacturing/KPIKindSalesOrders/
	// KPIKindOperations already give. A dedicated Kind rather than folding
	// into KPIKindOperations: assets/maintenance is its own module with
	// its own tables, not a cross-cutting metric spanning two unrelated
	// packages the way KPIKindOperations' own doc comment explains
	// active_projects/overdue_tasks are.
	KPIKindAssets KPIKind = "assets"
	// KPIKindContracts (Contracts management/document workflows/legal
	// foundation batch): dispatches further on ContractMetric below -
	// both metrics query internal/contracts' own contract_registry table
	// directly, the same "own tables live outside this package, queried
	// the same way" reasoning KPIKindAssets' own doc comment gives. A
	// dedicated Kind rather than folding into KPIKindOperations for the
	// identical reasoning KPIKindAssets' own doc comment already gives:
	// contracts is its own module with its own table, not a cross-cutting
	// metric spanning two unrelated packages.
	KPIKindContracts KPIKind = "contracts"
	// KPIKindFinancialPlanning (Budget management/cost centers/financial
	// planning batch): dispatches further on FinancialPlanningMetric
	// below - both metrics are complex, multi-table aggregations
	// (budgets/budget_lines/journal_lines/cost_centers together), not a
	// single table's own simple count/sum the way KPIKindAssets/
	// KPIKindContracts' own metrics are, so this is its own Kind rather
	// than folded into either.
	KPIKindFinancialPlanning KPIKind = "financial_planning"
)

// InventoryMetric is KPIKindInventoryKPI's own descriptor field - which
// inventory-specific aggregation to compute (see computeInventoryKPI).
type InventoryMetric string

const (
	// InventoryMetricLowStockProducts: how many distinct products
	// currently have less on-hand stock (summed across every location)
	// than their own min_quantity threshold - the same condition
	// components/Inventory/InventoryManager.tsx's own client-side
	// low-stock highlight checks per product, computed here server-side
	// across the whole catalog for the dashboard tile.
	InventoryMetricLowStockProducts InventoryMetric = "low_stock_products"
	// InventoryMetricTotalInventoryValue: SUM(quantity * cost_price)
	// across every stock_levels row - the firm's total on-hand inventory
	// value at cost, not at sale price (cost_price, not unit_price -
	// matching standard inventory-valuation practice: an unsold unit is
	// worth what it cost the firm, not what it might sell for).
	InventoryMetricTotalInventoryValue InventoryMetric = "total_inventory_value"
	// InventoryMetricPendingDeliveries: how many deliveries currently sit
	// in status 'pending' or 'in_transit' (i.e. not yet delivered/returned/
	// cancelled). Classified under KPIKindInventoryKPI, not a new
	// KPIKindLogistics: this is an OPERATIONAL fulfillment metric - "how
	// much outbound work is still in flight" - the same operational
	// register low_stock_products/total_inventory_value already occupy
	// (warehouse/fulfillment state), not a customer-RELATIONSHIP metric
	// the way active_customers is (see KPIKindCRM's own doc comment for
	// that contrast). A firm's inventory dashboard and its delivery
	// backlog are naturally read together - both answer "what does
	// operations need to act on today" - so sharing one Kind here avoids
	// a third Kind for what is, structurally, the exact same "count rows
	// matching a status/threshold condition" shape low_stock_products
	// already has.
	InventoryMetricPendingDeliveries InventoryMetric = "pending_deliveries"
)

// CRMMetric is KPIKindCRM's own descriptor field - which CRM-specific
// aggregation to compute (see computeCRMKPI). Kept as its own named type,
// the same "room to grow without a new Kind" pattern InventoryMetric
// already establishes, even though this batch defines only one CRMMetric
// today.
type CRMMetric string

const (
	// CRMMetricActiveCustomers: how many customers rows have a non-null
	// source_workflow_instance - i.e. converted from the customer_pipeline
	// workflow, not created directly via the HTTP API (see
	// migrations/0013_logistics_crm_core.up.sql's own doc comment on this
	// distinction).
	CRMMetricActiveCustomers CRMMetric = "active_customers"
	// CRMMetricPipelineValue (Multi-location CRM/project management
	// batch): SUM(crm_opportunities.value) WHERE stage NOT IN
	// ('won', 'lost') - the total value of deals still open, per the
	// design brief's "pipeline_value" spec. Folded into KPIKindCRM (not a
	// new Kind) since crm_opportunities is squarely the same CRM domain
	// customers already occupies here.
	CRMMetricPipelineValue CRMMetric = "pipeline_value"
	// CRMMetricWonThisMonth: COUNT(crm_opportunities) WHERE stage='won'
	// AND updated_at falls in the current calendar month - updated_at,
	// not created_at, since "won this month" means the deal CLOSED this
	// month (UpdateOpportunityStage bumps updated_at on every stage
	// change, including the one that sets stage='won'), regardless of
	// when the opportunity was first created.
	CRMMetricWonThisMonth CRMMetric = "won_this_month"
)

// SalesOrdersMetric is KPIKindSalesOrders' own descriptor field - which
// sales-order-specific figure to compute, the same "room to grow without
// a new Kind" pattern InventoryMetric/CRMMetric/ReceivablesMetric already
// establish.
type SalesOrdersMetric string

const (
	// SalesOrdersMetricOpenOrders: how many sales_orders are currently in
	// draft/confirmed/picking - "open" meaning not yet shipped, delivered,
	// or cancelled, per the design brief's "open_sales_orders" spec.
	SalesOrdersMetricOpenOrders SalesOrdersMetric = "open_orders"
	// SalesOrdersMetricSalesThisMonth: SUM(sales_orders.total) for orders
	// created in the current calendar month, per the design brief's
	// "sales_this_month" spec.
	SalesOrdersMetricSalesThisMonth SalesOrdersMetric = "sales_this_month"
)

// ManufacturingMetric is KPIKindManufacturing's own descriptor field -
// which manufacturing-specific figure to compute, the same "room to grow
// without a new Kind" pattern InventoryMetric/CRMMetric/ReceivablesMetric/
// SalesOrdersMetric already establish.
type ManufacturingMetric string

const (
	// ManufacturingMetricActiveProductionOrders: how many
	// production_orders are currently in planned/in_progress, per the
	// design brief's "active_production_orders" spec. (This batch's own
	// "production_wip_value" KPI is NOT a ManufacturingMetric - it's a
	// plain KPIKindAccountBalanceNow descriptor against
	// accounting.WorkInProgressAccountCode, since WIP's balance already
	// lives in journal_entries/journal_lines, the exact data
	// KPIKindAccountBalanceNow already knows how to sum; no new query
	// shape was needed for it.)
	ManufacturingMetricActiveProductionOrders ManufacturingMetric = "active_production_orders"
)

// OperationsMetric is KPIKindOperations' own descriptor field - which
// cross-cutting operational figure to compute, the same "room to grow
// without a new Kind" pattern InventoryMetric/CRMMetric/ManufacturingMetric
// already establish.
type OperationsMetric string

const (
	// OperationsMetricActiveProjects: how many projects rows have
	// status='active', per the design brief's "active_projects" spec.
	OperationsMetricActiveProjects OperationsMetric = "active_projects"
	// OperationsMetricOverdueTasks: how many task_approval instances sit
	// in a non-terminal state AND were created more than 30 days ago.
	// This is a heuristic, not a real due-date check: task_approval has
	// no due_date field (see TaskApprovalSpec, task_approval.go - its
	// payload only carries assignee_person_id/project_id), so "overdue"
	// here means "still open a long time after creation," a proxy for
	// "probably stale," not "past an actual deadline the requester set."
	// Document this on the frontend tile too (a tooltip, not just this
	// comment) so a firm doesn't read it as a precise SLA breach count.
	OperationsMetricOverdueTasks OperationsMetric = "overdue_tasks"
)

// AssetMetric is KPIKindAssets' own descriptor field - which
// asset/maintenance-specific figure to compute (see computeAssetKPI).
type AssetMetric string

const (
	// AssetMetricDueMaintenance: how many active maintenance_schedules
	// rows have next_due_at <= now() + 7 days, per the design brief's
	// "assets_due_maintenance" spec - the exact same lookahead window
	// internal/asset's own background scheduler (maintenanceDueLookaheadDays)
	// uses to decide when to notify, so this tile and that scheduler always
	// agree on what counts as "coming due."
	AssetMetricDueMaintenance AssetMetric = "assets_due_maintenance"
	// AssetMetricInMaintenance: how many assets rows currently have
	// status='maintenance', per the design brief's "assets_in_maintenance"
	// spec.
	AssetMetricInMaintenance AssetMetric = "assets_in_maintenance"
)

// ContractMetric is KPIKindContracts' own descriptor field - which
// contract-specific figure to compute (see computeContractKPI).
type ContractMetric string

const (
	// ContractMetricExpiringSoon: how many active, non-auto-renewing
	// contracts have end_date within the next 30 days, per the design
	// brief's "contracts_expiring_soon" spec - a fixed 30-day window
	// (unlike the scheduler's own per-contract renewal_notice_days),
	// matching the brief's literal spec text.
	ContractMetricExpiringSoon ContractMetric = "contracts_expiring_soon"
	// ContractMetricActive: total count of contracts with status='active',
	// per the design brief's "contracts_active" spec.
	ContractMetricActive ContractMetric = "contracts_active"
)

// FinancialPlanningMetric is KPIKindFinancialPlanning's own descriptor
// field - which financial-planning-specific figure to compute (see
// computeFinancialPlanningKPI).
type FinancialPlanningMetric string

const (
	// FinancialPlanningMetricBudgetUtilization: for the firm's currently
	// active budget, total actual spend (expense accounts only) / total
	// budgeted spend, as a percentage - per the design brief's
	// "budget_utilization" spec. "" (empty Value, not "0" or an error)
	// when the firm has no active budget, or its budget_lines sum to
	// zero - a real "nothing to report yet" answer, not a divide-by-zero
	// masquerading as 0%.
	FinancialPlanningMetricBudgetUtilization FinancialPlanningMetric = "budget_utilization"
	// FinancialPlanningMetricLargestCostCenterSpend: the cost center with
	// the highest total expense-account journal lines this calendar
	// month, per the design brief's "largest_cost_center_spend" spec -
	// name + amount, the one KPI in this codebase whose Value is
	// inherently a name/amount pair rather than a single scalar (see
	// KPIResult's own Label field, added for exactly this).
	FinancialPlanningMetricLargestCostCenterSpend FinancialPlanningMetric = "largest_cost_center_spend"
)

// ReceivablesMetric is KPIKindReceivables' own descriptor field - which
// receivables-specific aggregation to compute (see computeReceivablesKPI).
type ReceivablesMetric string

const (
	// ReceivablesMetricOverdueInvoices: how many invoices are either
	// explicitly status='overdue', or still status='sent' but past their
	// own due_date as of today - the same "an invoice can be overdue in
	// substance before anything has manually flipped its status" reasoning
	// internal/invoicing.ReceivablesAging's own bucket-by-age query
	// already applies (an invoice doesn't need a cron job or a manual
	// PATCH to "count" as overdue for this KPI).
	ReceivablesMetricOverdueInvoices ReceivablesMetric = "overdue_invoices"
	// ReceivablesMetricTotalOutstanding: SUM(invoice.total minus that
	// invoice's own recorded payments) across every unpaid invoice
	// ('sent' or 'overdue' - 'draft' isn't a real receivable yet, 'paid'/
	// 'cancelled' are no longer outstanding). Computed entirely in
	// Postgres's own exact `numeric` arithmetic (never a Go float), same
	// discipline internal/invoicing.recomputeInvoiceTotalsTx's own doc
	// comment establishes.
	ReceivablesMetricTotalOutstanding ReceivablesMetric = "total_outstanding"
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

	// InventoryMetric is used by KPIKindInventoryKPI.
	InventoryMetric InventoryMetric

	// CRMMetric is used by KPIKindCRM.
	CRMMetric CRMMetric

	// ReceivablesMetric is used by KPIKindReceivables.
	ReceivablesMetric ReceivablesMetric

	// SalesOrdersMetric is used by KPIKindSalesOrders.
	SalesOrdersMetric SalesOrdersMetric

	// ManufacturingMetric is used by KPIKindManufacturing.
	ManufacturingMetric ManufacturingMetric

	// OperationsMetric is used by KPIKindOperations.
	OperationsMetric OperationsMetric

	// AssetMetric is used by KPIKindAssets.
	AssetMetric AssetMetric

	// ContractMetric is used by KPIKindContracts.
	ContractMetric ContractMetric

	// FinancialPlanningMetric is used by KPIKindFinancialPlanning.
	FinancialPlanningMetric FinancialPlanningMetric
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
	{
		Key: "lowStockProducts", Kind: KPIKindInventoryKPI,
		InventoryMetric: InventoryMetricLowStockProducts,
	},
	{
		Key: "totalInventoryValue", Kind: KPIKindInventoryKPI,
		InventoryMetric: InventoryMetricTotalInventoryValue,
	},
	{
		Key: "pendingDeliveries", Kind: KPIKindInventoryKPI,
		InventoryMetric: InventoryMetricPendingDeliveries,
	},
	{
		Key: "activeCustomers", Kind: KPIKindCRM,
		CRMMetric: CRMMetricActiveCustomers,
	},
	{
		Key: "overdueInvoices", Kind: KPIKindReceivables,
		ReceivablesMetric: ReceivablesMetricOverdueInvoices,
	},
	{
		Key: "totalOutstanding", Kind: KPIKindReceivables,
		ReceivablesMetric: ReceivablesMetricTotalOutstanding,
	},
	{
		Key: "openSalesOrders", Kind: KPIKindSalesOrders,
		SalesOrdersMetric: SalesOrdersMetricOpenOrders,
	},
	{
		Key: "salesThisMonth", Kind: KPIKindSalesOrders,
		SalesOrdersMetric: SalesOrdersMetricSalesThisMonth,
	},
	{
		Key: "activeProductionOrders", Kind: KPIKindManufacturing,
		ManufacturingMetric: ManufacturingMetricActiveProductionOrders,
	},
	{
		Key: "productionWipValue", Kind: KPIKindAccountBalanceNow,
		AccountCodes: []string{accounting.WorkInProgressAccountCode}, NormalDebit: true,
	},
	{
		Key: "pipelineValue", Kind: KPIKindCRM,
		CRMMetric: CRMMetricPipelineValue,
	},
	{
		Key: "wonThisMonth", Kind: KPIKindCRM,
		CRMMetric: CRMMetricWonThisMonth,
	},
	{
		Key: "activeProjects", Kind: KPIKindOperations,
		OperationsMetric: OperationsMetricActiveProjects,
	},
	{
		Key: "overdueTasks", Kind: KPIKindOperations,
		OperationsMetric: OperationsMetricOverdueTasks,
	},
	{
		Key: "assetsDueMaintenance", Kind: KPIKindAssets,
		AssetMetric: AssetMetricDueMaintenance,
	},
	{
		Key: "assetsInMaintenance", Kind: KPIKindAssets,
		AssetMetric: AssetMetricInMaintenance,
	},
	{
		Key: "contractsExpiringSoon", Kind: KPIKindContracts,
		ContractMetric: ContractMetricExpiringSoon,
	},
	{
		Key: "contractsActive", Kind: KPIKindContracts,
		ContractMetric: ContractMetricActive,
	},
	{
		Key: "budgetUtilization", Kind: KPIKindFinancialPlanning,
		FinancialPlanningMetric: FinancialPlanningMetricBudgetUtilization,
	},
	{
		Key: "largestCostCenterSpend", Kind: KPIKindFinancialPlanning,
		FinancialPlanningMetric: FinancialPlanningMetricLargestCostCenterSpend,
	},
}

// KPIResult is one computed dashboard tile - Key ties it back to its
// KPIDescriptor (and the frontend's i18n label), Value is the computed
// figure (a plain decimal or integer string - see each Kind's own
// compute function for which), Unit distinguishes how to render/format
// it client-side.
type KPIResult struct {
	Key   string
	Unit  string // "currency" | "count" | "percent"
	Value string
	// Label (budget management/cost centers/financial planning batch) is
	// set only by FinancialPlanningMetricLargestCostCenterSpend - the one
	// KPI whose natural answer pairs a name with an amount (Value), not
	// just a bare scalar. "" for every other KPI in this codebase, the
	// same "one extra optional descriptor field used by exactly one
	// Kind" pattern KPIDescriptor's own per-Kind metric fields (e.g.
	// ContractMetric, AssetMetric) already establish.
	Label string
}

const (
	unitCurrency = "currency"
	unitCount    = "count"
	unitPercent  = "percent"
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

	case KPIKindInventoryKPI:
		return computeInventoryKPI(ctx, tx, firmID, d)

	case KPIKindCRM:
		return computeCRMKPI(ctx, tx, firmID, d)

	case KPIKindReceivables:
		return computeReceivablesKPI(ctx, tx, firmID, d)

	case KPIKindSalesOrders:
		return computeSalesOrdersKPI(ctx, tx, firmID, d)

	case KPIKindManufacturing:
		return computeManufacturingKPI(ctx, tx, firmID, d)

	case KPIKindOperations:
		return computeOperationsKPI(ctx, tx, firmID, d)

	case KPIKindAssets:
		return computeAssetKPI(ctx, tx, firmID, d)

	case KPIKindContracts:
		return computeContractKPI(ctx, tx, firmID, d)

	case KPIKindFinancialPlanning:
		return computeFinancialPlanningKPI(ctx, tx, firmID, d)

	default:
		return KPIResult{}, fmt.Errorf("unknown KPI kind %q", d.Kind)
	}
}

// computeInventoryKPI dispatches on d.InventoryMetric - the one place a
// new inventory metric (as opposed to a new descriptor of an existing
// metric, which doesn't exist here since there's exactly one descriptor
// per metric today) needs a new case, the same role computeKPI's own
// switch plays for KPIKind at the outer level.
//
// ciaudit:ignore-firmid-check: internal helper called only by computeKPI,
// itself only called by GetDashboardKPIs after permission.IsMember has
// already run - firmID here scopes each query (defense in depth alongside
// RLS), it is not itself an authorization decision.
func computeInventoryKPI(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, d KPIDescriptor) (KPIResult, error) {
	switch d.InventoryMetric {
	case InventoryMetricLowStockProducts:
		// LEFT JOIN (not JOIN): a product with zero stock_levels rows at
		// all - never sold, never received - still has on-hand quantity 0,
		// which is < any positive min_quantity threshold, so it must still
		// count as low stock; an inner join would silently exclude it.
		// min_quantity > 0 excludes products nobody has configured a real
		// threshold for (default 0, see products.min_quantity's own doc
		// comment, migrations/0011_inventory_core.up.sql) - "never
		// flagged low" is min_quantity's own documented default behavior,
		// not a gap in this query.
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM (
				SELECT p.id
				FROM products p
				LEFT JOIN stock_levels sl ON sl.product_id = p.id AND sl.firm_id = p.firm_id
				WHERE p.firm_id = $1 AND p.min_quantity > 0
				GROUP BY p.id, p.min_quantity
				HAVING COALESCE(SUM(sl.quantity), 0) < p.min_quantity
			) low_stock
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count low stock products: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	case InventoryMetricTotalInventoryValue:
		// COALESCE(p.cost_price, 0): a product with no cost_price set
		// (optional, see products.cost_price's own column) contributes
		// nothing to this total rather than making the whole SUM NULL -
		// the same "missing data reads back as zero, not an error"
		// convention accountBalanceSum's own doc comment establishes for
		// COALESCE(SUM(...), 0) below.
		// ::numeric(19,4) rounds the product (quantity * cost_price, both
		// numeric(19,4), naturally yields 8 fraction digits) back down to
		// the same 4-fraction-digit precision every other currency figure
		// in this codebase uses (matching amountPattern's own contract).
		var value string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(sl.quantity * COALESCE(p.cost_price, 0)), 0)::numeric(19,4)::text
			FROM stock_levels sl
			JOIN products p ON p.id = sl.product_id
			WHERE sl.firm_id = $1
		`, firmID).Scan(&value); err != nil {
			return KPIResult{}, fmt.Errorf("sum total inventory value: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCurrency, Value: value}, nil

	case InventoryMetricPendingDeliveries:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM deliveries WHERE firm_id = $1 AND status IN ('pending', 'in_transit')
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count pending deliveries: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	default:
		return KPIResult{}, fmt.Errorf("unknown inventory metric %q", d.InventoryMetric)
	}
}

// computeCRMKPI dispatches on d.CRMMetric - the same role computeInventoryKPI's
// own switch plays for InventoryMetric.
//
// ciaudit:ignore-firmid-check: internal helper called only by computeKPI,
// itself only called by GetDashboardKPIs after permission.IsMember has
// already run - firmID here scopes the query (defense in depth alongside
// RLS), it is not itself an authorization decision.
func computeCRMKPI(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, d KPIDescriptor) (KPIResult, error) {
	switch d.CRMMetric {
	case CRMMetricActiveCustomers:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM customers WHERE firm_id = $1 AND source_workflow_instance IS NOT NULL
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count active customers: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	case CRMMetricPipelineValue:
		var value string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(value), 0)::text FROM crm_opportunities WHERE firm_id = $1 AND stage NOT IN ('won', 'lost')
		`, firmID).Scan(&value); err != nil {
			return KPIResult{}, fmt.Errorf("sum pipeline value: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCurrency, Value: value}, nil

	case CRMMetricWonThisMonth:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM crm_opportunities
			WHERE firm_id = $1 AND stage = 'won' AND date_trunc('month', updated_at) = date_trunc('month', now())
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count won this month: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	default:
		return KPIResult{}, fmt.Errorf("unknown CRM metric %q", d.CRMMetric)
	}
}

// computeReceivablesKPI dispatches on d.ReceivablesMetric - the same role
// computeInventoryKPI/computeCRMKPI's own switches play for their metric
// types.
//
// ciaudit:ignore-firmid-check: internal helper called only by computeKPI,
// itself only called by GetDashboardKPIs after permission.IsMember has
// already run - firmID here scopes each query (defense in depth alongside
// RLS), it is not itself an authorization decision.
func computeReceivablesKPI(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, d KPIDescriptor) (KPIResult, error) {
	switch d.ReceivablesMetric {
	case ReceivablesMetricOverdueInvoices:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM invoices
			WHERE firm_id = $1 AND (status = 'overdue' OR (status = 'sent' AND due_date < CURRENT_DATE))
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count overdue invoices: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	case ReceivablesMetricTotalOutstanding:
		// GREATEST(..., 0): an overpaid invoice (this batch does not
		// reject overpayment, see internal/invoicing.RecordPayment's own
		// doc comment) would otherwise contribute a negative "outstanding"
		// amount to the sum - defense in depth, since an overpaid invoice
		// also auto-closes to 'paid' and so is excluded by the WHERE
		// clause below anyway in the ordinary case.
		var value string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(GREATEST(i.total - COALESCE(p.paid, 0), 0)), 0)::numeric(19,4)::text
			FROM invoices i
			LEFT JOIN (
				SELECT invoice_id, SUM(amount) AS paid FROM payments GROUP BY invoice_id
			) p ON p.invoice_id = i.id
			WHERE i.firm_id = $1 AND i.status IN ('sent', 'overdue')
		`, firmID).Scan(&value); err != nil {
			return KPIResult{}, fmt.Errorf("sum total outstanding: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCurrency, Value: value}, nil

	default:
		return KPIResult{}, fmt.Errorf("unknown receivables metric %q", d.ReceivablesMetric)
	}
}

// computeSalesOrdersKPI dispatches on d.SalesOrdersMetric - the same role
// computeReceivablesKPI's own switch plays for ReceivablesMetric.
//
// ciaudit:ignore-firmid-check: internal helper called only by computeKPI,
// itself only called by GetDashboardKPIs after permission.IsMember has
// already run - firmID here scopes each query (defense in depth alongside
// RLS), it is not itself an authorization decision.
func computeSalesOrdersKPI(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, d KPIDescriptor) (KPIResult, error) {
	switch d.SalesOrdersMetric {
	case SalesOrdersMetricOpenOrders:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM sales_orders
			WHERE firm_id = $1 AND status IN ('draft', 'confirmed', 'picking')
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count open sales orders: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	case SalesOrdersMetricSalesThisMonth:
		// date_trunc('month', now()): the current calendar month in the
		// database's own session time zone - same "compute period bounds
		// in SQL, not Go" discipline periodBounds/accountBalanceSum
		// already follow elsewhere in this file, so this KPI and any
		// other month-scoped one agree on exactly where a month begins.
		var value string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(total), 0)::numeric(19,4)::text
			FROM sales_orders
			WHERE firm_id = $1
			  AND status != 'cancelled'
			  AND created_at >= date_trunc('month', now())
			  AND created_at < date_trunc('month', now()) + interval '1 month'
		`, firmID).Scan(&value); err != nil {
			return KPIResult{}, fmt.Errorf("sum sales this month: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCurrency, Value: value}, nil

	default:
		return KPIResult{}, fmt.Errorf("unknown sales orders metric %q", d.SalesOrdersMetric)
	}
}

// computeManufacturingKPI dispatches on d.ManufacturingMetric - the same
// role computeSalesOrdersKPI's own switch plays for SalesOrdersMetric.
//
// ciaudit:ignore-firmid-check: internal helper called only by computeKPI,
// itself only called by GetDashboardKPIs after permission.IsMember has
// already run - firmID here scopes the query (defense in depth alongside
// RLS), it is not itself an authorization decision.
func computeManufacturingKPI(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, d KPIDescriptor) (KPIResult, error) {
	switch d.ManufacturingMetric {
	case ManufacturingMetricActiveProductionOrders:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM production_orders
			WHERE firm_id = $1 AND status IN ('planned', 'in_progress')
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count active production orders: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	default:
		return KPIResult{}, fmt.Errorf("unknown manufacturing metric %q", d.ManufacturingMetric)
	}
}

// computeOperationsKPI dispatches on d.OperationsMetric - the same role
// computeManufacturingKPI's own switch plays for ManufacturingMetric.
//
// ciaudit:ignore-firmid-check: internal helper called only by computeKPI,
// itself only called by GetDashboardKPIs after permission.IsMember has
// already run - firmID here scopes the query (defense in depth alongside
// RLS), it is not itself an authorization decision.
func computeOperationsKPI(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, d KPIDescriptor) (KPIResult, error) {
	switch d.OperationsMetric {
	case OperationsMetricActiveProjects:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM projects WHERE firm_id = $1 AND status = 'active'
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count active projects: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	case OperationsMetricOverdueTasks:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			FROM workflow_instances wi
			JOIN workflow_definitions wd ON wd.id = wi.workflow_definition_id
			JOIN workflow_states ws ON ws.id = wi.current_state_id
			WHERE wi.firm_id = $1 AND wd.key = 'task_approval' AND NOT ws.is_terminal
			  AND wi.created_at < now() - interval '30 days'
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count overdue tasks: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	default:
		return KPIResult{}, fmt.Errorf("unknown operations metric %q", d.OperationsMetric)
	}
}

// computeAssetKPI dispatches on d.AssetMetric - the same role
// computeOperationsKPI's own switch plays for OperationsMetric.
//
// ciaudit:ignore-firmid-check: internal helper called only by computeKPI,
// itself only called by GetDashboardKPIs after permission.IsMember has
// already run - firmID here scopes the query (defense in depth alongside
// RLS), it is not itself an authorization decision.
func computeAssetKPI(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, d KPIDescriptor) (KPIResult, error) {
	switch d.AssetMetric {
	case AssetMetricDueMaintenance:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM maintenance_schedules
			WHERE firm_id = $1 AND is_active AND next_due_at IS NOT NULL AND next_due_at <= now() + interval '7 days'
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count assets due maintenance: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	case AssetMetricInMaintenance:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM assets WHERE firm_id = $1 AND status = 'maintenance'
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count assets in maintenance: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	default:
		return KPIResult{}, fmt.Errorf("unknown asset metric %q", d.AssetMetric)
	}
}

// computeContractKPI dispatches on d.ContractMetric - the same role
// computeAssetKPI's own switch plays for AssetMetric.
//
// ciaudit:ignore-firmid-check: internal helper called only by computeKPI,
// itself only called by GetDashboardKPIs after permission.IsMember has
// already run - firmID here scopes the query (defense in depth alongside
// RLS), it is not itself an authorization decision.
func computeContractKPI(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, d KPIDescriptor) (KPIResult, error) {
	switch d.ContractMetric {
	case ContractMetricExpiringSoon:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM contract_registry
			WHERE firm_id = $1 AND status = 'active' AND NOT auto_renewal
				AND end_date IS NOT NULL AND end_date <= now() + interval '30 days'
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count contracts expiring soon: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	case ContractMetricActive:
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM contract_registry WHERE firm_id = $1 AND status = 'active'
		`, firmID).Scan(&count); err != nil {
			return KPIResult{}, fmt.Errorf("count active contracts: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCount, Value: fmt.Sprintf("%d", count)}, nil

	default:
		return KPIResult{}, fmt.Errorf("unknown contract metric %q", d.ContractMetric)
	}
}

// computeFinancialPlanningKPI dispatches on d.FinancialPlanningMetric -
// the same role computeContractKPI's own switch plays for ContractMetric.
// Both metrics here are the "complex aggregations" the design brief's own
// wording calls out - multi-table joins across budgets/budget_lines/
// journal_lines/cost_centers, not a single table's own count/sum.
//
// ciaudit:ignore-firmid-check: internal helper called only by computeKPI,
// itself only called by GetDashboardKPIs after permission.IsMember has
// already run - firmID here scopes the query (defense in depth alongside
// RLS), it is not itself an authorization decision.
func computeFinancialPlanningKPI(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, d KPIDescriptor) (KPIResult, error) {
	switch d.FinancialPlanningMetric {
	case FinancialPlanningMetricBudgetUtilization:
		var budgetID uuid.UUID
		var periodStart, periodEnd time.Time
		err := tx.QueryRow(ctx, `
			SELECT id, period_start, period_end FROM budgets WHERE firm_id = $1 AND status = 'active' LIMIT 1
		`, firmID).Scan(&budgetID, &periodStart, &periodEnd)
		if errors.Is(err, pgx.ErrNoRows) {
			return KPIResult{Key: d.Key, Unit: unitPercent, Value: ""}, nil
		}
		if err != nil {
			return KPIResult{}, fmt.Errorf("look up active budget: %w", err)
		}

		var plannedSpend string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(bl.planned_amount), 0)::text
			FROM budget_lines bl JOIN accounts a ON a.id = bl.account_id
			WHERE bl.firm_id = $1 AND bl.budget_id = $2 AND a.type = 'expense'
		`, firmID, budgetID).Scan(&plannedSpend); err != nil {
			return KPIResult{}, fmt.Errorf("sum planned expense spend: %w", err)
		}
		if plannedSpend == "0" || plannedSpend == "0.0000" {
			return KPIResult{Key: d.Key, Unit: unitPercent, Value: ""}, nil
		}

		var actualSpend string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(CASE WHEN jl.side = 'debit' THEN jl.amount WHEN jl.side = 'credit' THEN -jl.amount ELSE 0 END), 0)::text
			FROM journal_lines jl JOIN journal_entries je ON je.id = jl.entry_id JOIN accounts a ON a.id = jl.account_id
			WHERE jl.firm_id = $1 AND a.type = 'expense'
				AND je.posted_at >= $2::date AND je.posted_at < ($3::date + INTERVAL '1 day')
		`, firmID, periodStart, periodEnd).Scan(&actualSpend); err != nil {
			return KPIResult{}, fmt.Errorf("sum actual expense spend: %w", err)
		}

		var percent string
		if err := tx.QueryRow(ctx, `SELECT ($1::numeric / $2::numeric * 100)::text`, actualSpend, plannedSpend).Scan(&percent); err != nil {
			return KPIResult{}, fmt.Errorf("compute budget utilization: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitPercent, Value: percent}, nil

	case FinancialPlanningMetricLargestCostCenterSpend:
		var name, amount string
		err := tx.QueryRow(ctx, `
			SELECT cc.name, SUM(CASE WHEN jl.side = 'debit' THEN jl.amount WHEN jl.side = 'credit' THEN -jl.amount ELSE 0 END) AS spend
			FROM journal_lines jl
			JOIN journal_entries je ON je.id = jl.entry_id
			JOIN accounts a ON a.id = jl.account_id
			JOIN cost_centers cc ON cc.id = jl.cost_center_id
			WHERE jl.firm_id = $1 AND a.type = 'expense'
				AND je.posted_at >= date_trunc('month', now()) AND je.posted_at < date_trunc('month', now()) + INTERVAL '1 month'
			GROUP BY cc.id, cc.name
			ORDER BY spend DESC
			LIMIT 1
		`, firmID).Scan(&name, &amount)
		if errors.Is(err, pgx.ErrNoRows) {
			return KPIResult{Key: d.Key, Unit: unitCurrency, Value: ""}, nil
		}
		if err != nil {
			return KPIResult{}, fmt.Errorf("find largest cost center spend: %w", err)
		}
		return KPIResult{Key: d.Key, Unit: unitCurrency, Value: amount, Label: name}, nil

	default:
		return KPIResult{}, fmt.Errorf("unknown financial planning metric %q", d.FinancialPlanningMetric)
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
