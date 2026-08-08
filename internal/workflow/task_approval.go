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

// TaskApprovalKey is the workflow_definitions.key for the wizard content
// batch's (Open Points item 12) "do you manage tasks or approvals
// internally?" root question - universal across every kind of firm (asked
// regardless of the "do you sell products or services?" answer), so this
// is deliberately generic (a task, not a sale/lead/purchase-specific
// shape), same minimal 4-state scope boundary as PurchaseOrderSpec.
const TaskApprovalKey = "task_approval"

// Permission keys the Task/Approval workflow registers into the global
// permissions catalog - same naming convention as the other workflow spec
// files in this package.
const (
	CreateTaskPermission   = "workflow.task_approval.create"
	StartTaskPermission    = "workflow.task_approval.start"
	CompleteTaskPermission = "workflow.task_approval.complete"
	RejectTaskPermission   = "workflow.task_approval.reject"
)

// TaskApprovalSpec is the concrete "Open -> In Progress -> Done/Rejected"
// task/approval workflow: a task or approval request is opened, started,
// then either completed or rejected.
//
// Fields adds one OPTIONAL payload field (HR core batch): assignee_person_id,
// a FieldTypePerson reference to internal/hr's `people` table - "who this
// task is assigned to." Deliberately OPTIONAL (Required: false), not
// required: every CreateInstance call for this workflow that predates
// this batch (this package's own integration tests, the earlier E2E
// smoke test coverage) creates instances with no assignee at all, and
// Required: true here would break every one of them (Never-Violate Rule
// 6) - an unassigned task/approval request remains a fully valid state,
// same as before this field existed. When present, CreateInstance
// validates it against a real `people` row in this firm
// (checkPersonField, engine.go) the same way a FieldTypeReference field
// is validated against a real workflow instance.
var TaskApprovalSpec = DefinitionSpec{
	Key:  TaskApprovalKey,
	Name: "Task / Approval",
	CreatePermission: PermissionSpec{
		Key:         CreateTaskPermission,
		Description: "Create a new task or approval request (starts a Task / Approval workflow instance).",
	},
	Fields: []FieldSpec{
		{Name: "assignee_person_id", Type: FieldTypePerson, Required: false},
	},
	States: []StateSpec{
		{Key: "open", Name: "Open", IsInitial: true},
		{Key: "in_progress", Name: "In Progress"},
		{Key: "done", Name: "Done", IsTerminal: true},
		{Key: "rejected", Name: "Rejected", IsTerminal: true},
	},
	Transitions: []TransitionSpec{
		{
			FromStateKey: "open",
			ToStateKey:   "in_progress",
			ActionKey:    "start",
			Name:         "Start",
			Permission: PermissionSpec{
				Key:         StartTaskPermission,
				Description: "Start work on an open task or approval request.",
			},
		},
		{
			FromStateKey: "in_progress",
			ToStateKey:   "done",
			ActionKey:    "complete",
			Name:         "Complete",
			Permission: PermissionSpec{
				Key:         CompleteTaskPermission,
				Description: "Mark an in-progress task or approval request as done.",
			},
		},
		{
			FromStateKey: "in_progress",
			ToStateKey:   "rejected",
			ActionKey:    "reject",
			Name:         "Reject",
			Permission: PermissionSpec{
				Key:         RejectTaskPermission,
				Description: "Reject an in-progress task or approval request.",
			},
		},
	},
}

// SeedTaskApprovalWorkflow instantiates TaskApprovalSpec for firmID -
// test/fixture entry point, mirroring SeedStockToSaleWorkflow.
//
// ciaudit:ignore-firmid-check: thin wrapper around DefineWorkflow, same
// reasoning as SeedStockToSaleWorkflow - provisioning-only, never exposed
// via an HTTP handler.
func SeedTaskApprovalWorkflow(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflow(ctx, pool, firmID, TaskApprovalSpec)
}

// SeedTaskApprovalWorkflowTx is SeedTaskApprovalWorkflow's DefineWorkflowTx
// counterpart, for the firm-creation wizard - see internal/wizard.CreateDefaultFirm.
//
// ciaudit:ignore-firmid-check: thin wrapper around DefineWorkflowTx, same
// reasoning as SeedStockToSaleWorkflowTx - only called by
// internal/wizard.CreateDefaultFirm with a firmID it just created, and
// test fixtures.
func SeedTaskApprovalWorkflowTx(ctx context.Context, tx pgx.Tx, firmID, granteeRoleID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflowTx(ctx, tx, firmID, granteeRoleID, TaskApprovalSpec)
}
