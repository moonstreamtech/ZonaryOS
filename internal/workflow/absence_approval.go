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

// AbsenceApprovalKey is the workflow_definitions.key for the payroll
// foundation + time tracking + HR depth batch's absence-approval workflow -
// mirrors TaskApprovalKey exactly (task_approval.go), scoped down to just
// the two-terminal-state "requested -> approved/rejected" shape an
// absence request needs. internal/absence backs every absence request
// with a real instance of this workflow, so approving/rejecting an
// absence reuses the SAME pending_approvals + notification mechanism
// (approvals.go) every other approval in this codebase goes through,
// rather than a bespoke parallel one.
const AbsenceApprovalKey = "absence_approval"

// Permission keys the Absence Approval workflow registers into the global
// permissions catalog - same naming convention as TaskApprovalKey's own
// constants.
const (
	RequestAbsencePermission = "workflow.absence_approval.request"
	ApproveAbsencePermission = "workflow.absence_approval.approve"
	RejectAbsencePermission  = "workflow.absence_approval.reject"
)

// AbsenceApprovalSpec is the concrete "Requested -> Approved/Rejected"
// absence-approval workflow: an absence request starts at `requested`
// and is either approved or rejected - no intermediate "in progress"
// state (unlike TaskApprovalSpec), since an absence request has nothing
// analogous to "work starting."
var AbsenceApprovalSpec = DefinitionSpec{
	Key:  AbsenceApprovalKey,
	Name: "Absence Approval",
	CreatePermission: PermissionSpec{
		Key:         RequestAbsencePermission,
		Description: "Request a new absence (starts an Absence Approval workflow instance).",
	},
	States: []StateSpec{
		{Key: "requested", Name: "Requested", IsInitial: true},
		{Key: "approved", Name: "Approved", IsTerminal: true},
		{Key: "rejected", Name: "Rejected", IsTerminal: true},
	},
	Transitions: []TransitionSpec{
		{
			FromStateKey: "requested",
			ToStateKey:   "approved",
			ActionKey:    "approve",
			Name:         "Approve",
			Permission: PermissionSpec{
				Key:         ApproveAbsencePermission,
				Description: "Approve a requested absence.",
			},
		},
		{
			FromStateKey: "requested",
			ToStateKey:   "rejected",
			ActionKey:    "reject",
			Name:         "Reject",
			Permission: PermissionSpec{
				Key:         RejectAbsencePermission,
				Description: "Reject a requested absence.",
			},
		},
	},
}

// SeedAbsenceApprovalWorkflow instantiates AbsenceApprovalSpec for firmID -
// test/fixture entry point, mirroring SeedTaskApprovalWorkflow.
//
// ciaudit:ignore-firmid-check: thin wrapper around DefineWorkflow, same
// reasoning as SeedTaskApprovalWorkflow - provisioning-only, never exposed
// via an HTTP handler.
func SeedAbsenceApprovalWorkflow(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflow(ctx, pool, firmID, AbsenceApprovalSpec)
}

// SeedAbsenceApprovalWorkflowTx is SeedAbsenceApprovalWorkflow's
// DefineWorkflowTx counterpart, for the firm-creation wizard - see
// internal/wizard.CreateDefaultFirm.
//
// ciaudit:ignore-firmid-check: thin wrapper around DefineWorkflowTx, same
// reasoning as SeedTaskApprovalWorkflowTx - only called by
// internal/wizard.CreateDefaultFirm with a firmID it just created, and
// test fixtures.
func SeedAbsenceApprovalWorkflowTx(ctx context.Context, tx pgx.Tx, firmID, granteeRoleID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflowTx(ctx, tx, firmID, granteeRoleID, AbsenceApprovalSpec)
}
