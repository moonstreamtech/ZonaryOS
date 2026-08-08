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
	// Fields adds several OPTIONAL payload fields (Inventory/Logistics/CRM
	// batches, same "optional and additive" contract PurchaseOrderSpec.Fields'
	// own doc comment already establishes) - product_id (the real
	// internal/inventory.Product this sale is against), customer_id (the
	// real internal/crm.Customer this sale is for), and four plain string
	// fields (destination_address/origin_address/carrier/tracking_number)
	// feeding the Delivery template below. None are Required=true: every
	// ExecuteTransition call that predates these fields (this package's
	// own tests, the E2E smoke test's earlier stock_to_sale coverage)
	// records a sale with none of them at all, and Required=true on any
	// would break every one of them (Rule 6). "quantity"/"unit_price"
	// themselves are NOT declared here - they already exist as freeform
	// payload fields (see the Journal template above) and stay exactly
	// that way; only the fields below are new.
	Fields: []FieldSpec{
		{Name: "product_id", Type: FieldTypeProduct, Required: false},
		{Name: "customer_id", Type: FieldTypeCustomer, Required: false},
		{Name: "destination_address", Type: FieldTypeString, Required: false},
		{Name: "origin_address", Type: FieldTypeString, Required: false},
		{Name: "carrier", Type: FieldTypeString, Required: false},
		{Name: "tracking_number", Type: FieldTypeString, Required: false},
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
			// The workflow-to-inventory bridge (Inventory management
			// batch), wired the exact same way the Journal template just
			// above is: when both product_id and quantity are present on
			// this call, ExecuteTransition decreases stock_levels.quantity
			// by quantity, in the SAME transaction as this journal entry
			// and the state change itself (engine.go's
			// resolveStockAdjustment/internal/inventory.AdjustStockTx) - a
			// sale that would take stock below zero fails the whole
			// transition (ErrInsufficientStock), not just this hook, per
			// the design brief's "reject, don't just warn". Left unset on
			// any call that predates this batch (no product_id supplied)
			// means "adjust nothing" - see StockAdjustmentTemplate's own
			// doc comment.
			StockAdjustment: &StockAdjustmentTemplate{
				ProductField:  "product_id",
				QuantityField: "quantity",
				Direction:     "decrease",
				Reason:        "sale",
			},
			// The workflow-to-logistics bridge (Logistics management
			// batch): "selling a product can automatically create a
			// delivery record - operational continuity without manual
			// steps" (design brief). When destination_address is present
			// on this call, ExecuteTransition creates a deliveries row
			// (status "pending") in the SAME transaction as the journal
			// entry, stock adjustment, and state change - see
			// DeliveryTemplate's own doc comment. Left unset on any call
			// that predates this batch (no destination_address supplied)
			// means "create nothing".
			Delivery: &DeliveryTemplate{
				DestinationAddressField: "destination_address",
				OriginAddressField:      "origin_address",
				CarrierField:            "carrier",
				TrackingNumberField:     "tracking_number",
				ReferenceField:          "item",
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
