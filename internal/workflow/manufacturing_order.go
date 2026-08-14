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

// ManufacturingOrderKey is the workflow_definitions.key for the
// manufacturing module foundation batch's "do you manufacture products?"
// root question. Deliberately a minimal, EFFECTS-FREE state machine - a
// parametric REPRESENTATION of the production order lifecycle, seeded
// alongside the real internal/manufacturing.ProductionOrder table that
// actually drives material issues, stock adjustments, and journal
// postings. Unlike stock_to_sale/purchase_order, this workflow's own
// transitions carry no bridge templates: internal/manufacturing's own
// Start/Complete/Cancel HTTP handlers are what perform the real work
// (AdjustStockTx/PostJournalEntryTx), directly against production_orders
// - not through ExecuteTransition. This definition exists purely so
// manufacturing shows up in the same workflow-definition-driven surfaces
// (the dashboard's per-definition overview, quick-create, global search)
// every other wizard-gated capability does, the same "real table plus a
// parallel workflow definition for consistency" shape
// internal/payroll/internal/timetracking chose NOT to need (no dashboard
// workflow-overview presence) but internal/absence DID need (a real
// workflow instance backs every absence request) - manufacturing sits
// between those two: a real table drives it, but a workflow definition
// still represents it for cross-module consistency, per this batch's own
// design brief ("a parametric representation of the production order
// lifecycle alongside the real production_orders table").
const ManufacturingOrderKey = "manufacturing_order"

// Permission keys the Manufacturing Order workflow registers into the
// global permissions catalog - same naming convention as every other
// workflow spec file in this package. Deliberately NOT what
// internal/manufacturing's own HTTP handlers check (that package defines
// and checks its own owner/member tiers directly, the same way
// internal/payroll's period-close does) - these exist solely so this
// workflow definition's own CreatePermission/TransitionSpec.Permission
// fields are non-empty, satisfying DefinitionSpec.Validate.
const (
	CreateManufacturingOrderPermission   = "workflow.manufacturing_order.create"
	StartManufacturingOrderPermission    = "workflow.manufacturing_order.start"
	CompleteManufacturingOrderPermission = "workflow.manufacturing_order.complete"
	CancelManufacturingOrderPermission   = "workflow.manufacturing_order.cancel"
)

// ManufacturingOrderSpec is the concrete "Planned -> In Progress ->
// Completed/Cancelled" manufacturing order workflow - see this file's own
// doc comment for why it carries no Effects.
var ManufacturingOrderSpec = DefinitionSpec{
	Key:  ManufacturingOrderKey,
	Name: "Manufacturing Order",
	CreatePermission: PermissionSpec{
		Key:         CreateManufacturingOrderPermission,
		Description: "Create a new manufacturing order (starts a Manufacturing Order workflow instance).",
	},
	States: []StateSpec{
		{Key: "planned", Name: "Planned", IsInitial: true},
		{Key: "in_progress", Name: "In Progress"},
		{Key: "completed", Name: "Completed", IsTerminal: true},
		{Key: "cancelled", Name: "Cancelled", IsTerminal: true},
	},
	Transitions: []TransitionSpec{
		{
			FromStateKey: "planned",
			ToStateKey:   "in_progress",
			ActionKey:    "start",
			Name:         "Start",
			Permission: PermissionSpec{
				Key:         StartManufacturingOrderPermission,
				Description: "Start a planned manufacturing order.",
			},
		},
		{
			FromStateKey: "in_progress",
			ToStateKey:   "completed",
			ActionKey:    "complete",
			Name:         "Complete",
			Permission: PermissionSpec{
				Key:         CompleteManufacturingOrderPermission,
				Description: "Complete an in-progress manufacturing order.",
			},
		},
		{
			FromStateKey: "planned",
			ToStateKey:   "cancelled",
			ActionKey:    "cancel",
			Name:         "Cancel",
			Permission: PermissionSpec{
				Key:         CancelManufacturingOrderPermission,
				Description: "Cancel a planned manufacturing order.",
			},
		},
		{
			FromStateKey: "in_progress",
			ToStateKey:   "cancelled",
			ActionKey:    "cancel",
			Name:         "Cancel",
			Permission: PermissionSpec{
				Key:         CancelManufacturingOrderPermission,
				Description: "Cancel an in-progress manufacturing order.",
			},
		},
	},
}

// SeedManufacturingOrderWorkflow instantiates ManufacturingOrderSpec for
// firmID - test/fixture entry point, mirroring SeedPurchaseOrderWorkflow.
//
// ciaudit:ignore-firmid-check: thin wrapper around DefineWorkflow, same
// reasoning as SeedPurchaseOrderWorkflow - provisioning-only, never
// exposed via an HTTP handler.
func SeedManufacturingOrderWorkflow(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflow(ctx, pool, firmID, ManufacturingOrderSpec)
}

// SeedManufacturingOrderWorkflowTx is SeedManufacturingOrderWorkflow's
// DefineWorkflowTx counterpart, for the firm-creation wizard - see
// internal/wizard.CreateDefaultFirm.
//
// ciaudit:ignore-firmid-check: thin wrapper around DefineWorkflowTx, same
// reasoning as SeedPurchaseOrderWorkflowTx - only called by
// internal/wizard.CreateDefaultFirm with a firmID it just created, and
// test fixtures.
func SeedManufacturingOrderWorkflowTx(ctx context.Context, tx pgx.Tx, firmID, granteeRoleID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflowTx(ctx, tx, firmID, granteeRoleID, ManufacturingOrderSpec)
}
