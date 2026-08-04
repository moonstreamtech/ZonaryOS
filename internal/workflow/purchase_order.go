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
// either received or cancelled. No payload schema of its own, same
// deliberate choice StockToSaleSpec/CustomerPipelineSpec already make -
// CreateInstanceForm's free-form editor covers a purchase order's fields
// (supplier, items, ...) without a hardcoded schema.
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
