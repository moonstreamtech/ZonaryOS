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
// Fields adds two OPTIONAL payload fields. assignee_person_id (HR core
// batch): a FieldTypePerson reference to internal/hr's `people` table -
// "who this task is assigned to." project_id (Multi-location CRM/project
// management batch): a FieldTypeProject reference to
// internal/project's `projects` table - "which project this task belongs
// to," internal/project.GetProjectSummary's own source of truth for a
// project's linked tasks (it filters workflow_instances by
// payload->>'project_id', not a real foreign key column). Both are
// deliberately OPTIONAL (Required: false), not required: every
// CreateInstance call for this workflow that predates each field (this
// package's own integration tests, the earlier E2E smoke test coverage)
// creates instances without them, and Required: true here would break
// every one of them (Never-Violate Rule 6) - an unassigned/unlinked
// task/approval request remains a fully valid state, same as before
// either field existed. When present, CreateInstance validates each
// against a real row in this firm (personValidator/projectValidator,
// validation.go) the same way a FieldTypeReference field is validated
// against a real workflow instance.
var TaskApprovalSpec = DefinitionSpec{
	Key:  TaskApprovalKey,
	Name: "Task / Approval",
	CreatePermission: PermissionSpec{
		Key:         CreateTaskPermission,
		Description: "Create a new task or approval request (starts a Task / Approval workflow instance).",
	},
	Fields: []FieldSpec{
		{Name: "assignee_person_id", Type: FieldTypePerson, Required: false},
		{Name: "project_id", Type: FieldTypeProject, Required: false},
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
