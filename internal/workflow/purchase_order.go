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

// PurchaseOrderKey is the workflow_definitions.key for the wizard content
// batch's (Open Points item 12) "do you purchase from suppliers?" root
// question - a deliberately minimal 3-state purchasing flow, not a full
// procurement module (see the design brief's scope boundary: "don't over-
// engineer").
const PurchaseOrderKey = "purchase_order"

// Permission keys the Purchase Order workflow registers into the global
// permissions catalog - same naming convention as stock_to_sale.go/
// customer_pipeline.go's own constants.
const (
	CreatePurchaseOrderPermission  = "workflow.purchase_order.create"
	SendPurchaseOrderPermission    = "workflow.purchase_order.send"
	ReceivePurchaseOrderPermission = "workflow.purchase_order.receive"
	CancelPurchaseOrderPermission  = "workflow.purchase_order.cancel"
)

// PurchaseOrderSpec is the concrete "Draft -> Sent -> Received/Cancelled"
// purchase order workflow: a draft order is sent to a supplier, then
// either received or cancelled.
//
// Fields adds two OPTIONAL payload fields (Open Points item 35's
// contract: optional and additive, see spec.go's DefinitionSpec.Fields
// doc comment) - supplier_name and total_amount, the two pieces of data
// the financial management core's bridge below actually needs. Neither
// is Required=true: every ExecuteTransition call that predates this
// batch (this package's own integration tests, the E2E smoke test's
// earlier "purchase_order" coverage) creates/transitions instances with
// neither field present, and Required=true here would break every one of
// them (Never-Violate Rule 6). CreateInstanceForm still renders both as
// real typed fields once a firm defines/uses this workflow through the
// UI - "optional" only means CreateInstance doesn't refuse an instance
// that omits them, not that the frontend hides them.
//
// The workflow-to-ledger bridge (financial management core, wired the
// same way stock_to_sale.go's record_sale is) is deliberately attached
// to ONLY the "receive" transition, not "send" - see the "receive"
// TransitionSpec's own doc comment below for the accounting reasoning.
var PurchaseOrderSpec = DefinitionSpec{
	Key:  PurchaseOrderKey,
	Name: "Purchase Order",
	CreatePermission: PermissionSpec{
		Key:         CreatePurchaseOrderPermission,
		Description: "Create a new purchase order (starts a Purchase Order workflow instance).",
	},
	States: []StateSpec{
		{Key: "draft", Name: "Draft", IsInitial: true},
		{Key: "sent", Name: "Sent"},
		{Key: "received", Name: "Received", IsTerminal: true},
		{Key: "cancelled", Name: "Cancelled", IsTerminal: true},
	},
	Fields: []FieldSpec{
		{Name: "supplier_name", Type: FieldTypeString, Required: false},
		{Name: "total_amount", Type: FieldTypeNumber, Required: false},
	},
	Transitions: []TransitionSpec{
		{
			FromStateKey: "draft",
			ToStateKey:   "sent",
			ActionKey:    "send",
			Name:         "Send",
			Permission: PermissionSpec{
				Key:         SendPurchaseOrderPermission,
				Description: "Send a draft purchase order to its supplier.",
			},
			// Deliberately no Journal here, despite the original design
			// brief proposing "DR Purchase Expense / CR Trade Payables"
			// at send-time. Sending a draft PO to a supplier is a
			// commercial communication, not a completed financial
			// transaction under accrual accounting: no goods or services
			// have actually changed hands yet, so nothing has actually
			// been bought - recording an expense (or any asset/liability)
			// here would violate the matching principle (expensing before
			// the corresponding goods/service is received) purely because
			// a document was sent. It would also double-count against
			// "receive" below: both transitions crediting Trade Payables
			// for the same purchase would leave the firm owing its
			// supplier twice for one order. See "receive"'s own doc
			// comment for where the real entry belongs.
		},
		{
			FromStateKey: "sent",
			ToStateKey:   "received",
			ActionKey:    "receive",
			Name:         "Receive",
			Permission: PermissionSpec{
				Key:         ReceivePurchaseOrderPermission,
				Description: "Mark a sent purchase order as received.",
			},
			// This is where the real financial event happens: goods are
			// actually received from the supplier, creating both a real
			// asset (inventory on hand) and a real liability (an
			// obligation to pay the supplier) at the same instant -
			// standard perpetual-inventory treatment for a purchase not
			// yet paid. DR Inventory / CR Trade Payables, for
			// total_amount. Account codes match
			// accounting.InventoryAccountCode/TradePayablesAccountCode -
			// both are seeded unconditionally whenever this workflow is
			// (see internal/accounting/seed.go's purchasesAccounts, which
			// includes InventoryAccountCode specifically so a firm that
			// purchases without also selling products still has
			// somewhere to post received goods).
			Journal: &JournalTemplate{
				Description: "Received purchase order from {{supplier_name}}",
				Lines: []LineTemplate{
					{AccountCode: accounting.InventoryAccountCode, Side: "debit", AmountField: "total_amount"},
					{AccountCode: accounting.TradePayablesAccountCode, Side: "credit", AmountField: "total_amount"},
				},
			},
		},
		{
			FromStateKey: "sent",
			ToStateKey:   "cancelled",
			ActionKey:    "cancel",
			Name:         "Cancel",
			Permission: PermissionSpec{
				Key:         CancelPurchaseOrderPermission,
				Description: "Cancel a sent purchase order.",
			},
		},
	},
}

// SeedPurchaseOrderWorkflow instantiates PurchaseOrderSpec for firmID -
// test/fixture entry point, mirroring SeedStockToSaleWorkflow.
//
// ciaudit:ignore-firmid-check: thin wrapper around DefineWorkflow, same
// reasoning as SeedStockToSaleWorkflow - provisioning-only, never exposed
// via an HTTP handler.
func SeedPurchaseOrderWorkflow(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflow(ctx, pool, firmID, PurchaseOrderSpec)
}

// SeedPurchaseOrderWorkflowTx is SeedPurchaseOrderWorkflow's DefineWorkflowTx
// counterpart, for the firm-creation wizard - see internal/wizard.CreateDefaultFirm.
//
// ciaudit:ignore-firmid-check: thin wrapper around DefineWorkflowTx, same
// reasoning as SeedStockToSaleWorkflowTx - only called by
// internal/wizard.CreateDefaultFirm with a firmID it just created, and
// test fixtures.
func SeedPurchaseOrderWorkflowTx(ctx context.Context, tx pgx.Tx, firmID, granteeRoleID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflowTx(ctx, tx, firmID, granteeRoleID, PurchaseOrderSpec)
}
