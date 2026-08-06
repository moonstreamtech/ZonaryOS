// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
)

// StockToSaleKey is the workflow_definitions.key for this PR's concrete
// first workflow instance. "Add stock" is instance creation (in_stock is
// the initial state); "record a sale" is the one transition, in_stock ->
// sold. No wizard integration yet - SeedStockToSaleWorkflow is the
// migration/fixture-level stand-in the wizard will eventually replace.
const StockToSaleKey = "stock_to_sale"

// Permission keys the Stock In -> Sale workflow registers into the global
// permissions catalog (see DefineWorkflow) - exported so callers and tests
// reference the same constants instead of duplicating string literals.
const (
	AddStockPermission   = "workflow.stock_to_sale.add_stock"
	RecordSalePermission = "workflow.stock_to_sale.record_sale"
)

// StockToSaleSpec is the concrete two-step "Stock In -> Sale" workflow
// described in the vertical slice's original scope.
var StockToSaleSpec = DefinitionSpec{
	Key:  StockToSaleKey,
	Name: "Stock In -> Sale",
	CreatePermission: PermissionSpec{
		Key:         AddStockPermission,
		Description: "Add a new stock item (starts a Stock In -> Sale workflow instance).",
	},
	States: []StateSpec{
		{Key: "in_stock", Name: "In Stock", IsInitial: true},
		{Key: "sold", Name: "Sold", IsTerminal: true},
	},
	Transitions: []TransitionSpec{
		{
			FromStateKey: "in_stock",
			ToStateKey:   "sold",
			ActionKey:    "record_sale",
			Name:         "Record Sale",
			Permission: PermissionSpec{
				Key:         RecordSalePermission,
				Description: "Record the sale of an in-stock item.",
			},
			// The workflow-to-ledger bridge's real working example
			// (Vision §3's financial management core): recording a sale
			// posts DR Trade Receivables / CR Sales Revenue, for
			// quantity * unit_price - both fields realistically present
			// on a sale ("quantity" is the field this package's own
			// tests already use for stock_to_sale instances, see
			// rules_integration_test.go; "unit_price" is this batch's
			// addition, since a sale amount needs a price, not just a
			// quantity). Account codes 1100/4000 match the core chart of
			// accounts internal/wizard.CreateDefaultFirm seeds via
			// internal/accounting.SeedDefaultChartOfAccountsTx whenever a
			// firm answers "yes" to selling products - the same
			// precondition (Sells && TracksInventory) that seeds this
			// workflow in the first place, so the accounts this template
			// names are always present when this workflow exists. Left
			// unset (nil) on any transition means "post nothing" - see
			// TransitionSpec.Journal's own doc comment - so a caller that
			// executes record_sale without supplying quantity/unit_price
			// (every call site that predates this batch) keeps working
			// exactly as before; the entry is only posted once both
			// fields are actually present (see resolveJournalLines in
			// engine.go).
			Journal: &JournalTemplate{
				Description: "Sale of {{item}}",
				Lines: []LineTemplate{
					{AccountCode: accounting.TradeReceivablesAccountCode, Side: "debit", AmountField: "quantity*unit_price"},
					{AccountCode: accounting.SalesRevenueAccountCode, Side: "credit", AmountField: "quantity*unit_price"},
				},
			},
		},
	},
}

// SeedStockToSaleWorkflow instantiates StockToSaleSpec for firmID,
// returning its workflow_definitions.id. Intended for test fixtures and
// firm provisioning until the wizard (a later PR) can define workflows
// like this one interactively.
//
// ciaudit:ignore-firmid-check: thin wrapper around DefineWorkflow, same
// reasoning - provisioning-only, never exposed via an HTTP handler.
func SeedStockToSaleWorkflow(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflow(ctx, pool, firmID, StockToSaleSpec)
}

// SeedStockToSaleWorkflowTx is SeedStockToSaleWorkflow's DefineWorkflowTx
// counterpart, for callers that need it as one step inside a larger
// transaction - namely the firm-creation wizard, which provisions this
// workflow atomically alongside the firm itself and its default role.
// granteeRoleID is granted every permission this workflow introduces (the
// self-action auto-grant DefineWorkflowTx implements) - the caller no
// longer needs a separate grant step of its own.
//
// ciaudit:ignore-firmid-check: thin wrapper around DefineWorkflowTx, same
// reasoning - only called by internal/wizard.CreateDefaultFirm with a
// firmID it just created, and test fixtures.
func SeedStockToSaleWorkflowTx(ctx context.Context, tx pgx.Tx, firmID, granteeRoleID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflowTx(ctx, tx, firmID, granteeRoleID, StockToSaleSpec)
}
