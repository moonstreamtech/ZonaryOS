// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow

import "errors"

var (
	// ErrDefinitionNotFound means no workflow_definitions row with the
	// given ID is visible in the caller's firm context (RLS: either it
	// doesn't exist, or it belongs to a different firm).
	ErrDefinitionNotFound = errors.New("workflow definition not found")

	// ErrInstanceNotFound means no workflow_instances row with the given
	// ID is visible in the caller's firm context.
	ErrInstanceNotFound = errors.New("workflow instance not found")

	// ErrNoSuchTransition means the instance's current state has no
	// transition for the requested action key - either the action key is
	// wrong, or it isn't valid from where the instance currently is.
	ErrNoSuchTransition = errors.New("no such transition from the instance's current state")

	// ErrPermissionDenied means the caller does not hold the permission
	// required for the action - checked via internal/permission against
	// their role(s) in this firm, never bypassed.
	ErrPermissionDenied = errors.New("caller does not have the required permission")

	// ErrInvalidSpec means a DefinitionSpec submitted to DefineWorkflowForFirm
	// failed DefinitionSpec.Validate() - structurally invalid (no initial
	// state, a transition referencing an unknown state, a duplicate
	// transition, ...), not a permission or existence problem.
	ErrInvalidSpec = errors.New("invalid workflow definition spec")

	// ErrDefinitionKeyExists means firmID already has a workflow_definitions
	// row with the requested key (UNIQUE (firm_id, key),
	// migrations/0003_workflow_engine.up.sql) - including the well-known
	// keys this codebase itself seeds (e.g. "stock_to_sale").
	ErrDefinitionKeyExists = errors.New("a workflow definition with this key already exists for this firm")

	// ErrPayloadValidation means a CreateInstance payload was rejected
	// against its definition's OPTIONAL payload schema (Open Points item
	// 35, spec.go's DefinitionSpec.Fields) - a required field was missing,
	// or a present field's value didn't match its declared FieldType. Only
	// ever returned for a definition that actually has a schema; a
	// schema-less definition (nil/empty Fields, e.g. StockToSaleSpec/
	// CustomerPipelineSpec today) never produces this error - see
	// CreateInstance's own doc comment.
	ErrPayloadValidation = errors.New("workflow instance payload failed schema validation")

	// ErrApprovalNotFound means no pending_approvals row with the given ID
	// is visible in the caller's firm context - same "member of firm but
	// this row doesn't exist" 404 convention as ErrInstanceNotFound.
	ErrApprovalNotFound = errors.New("pending approval not found")

	// ErrApprovalNotPending means Approve/Reject was called on an approval
	// that has already been resolved (approved or rejected) - a 409, not a
	// 404: the row exists and is visible, it just isn't actionable anymore.
	ErrApprovalNotPending = errors.New("pending approval has already been resolved")

	// ErrApprovalExpired means Approve/Reject was called on an approval
	// past its own expires_at - lazily transitioned to 'expired' status at
	// that point (see resolveApprovalTx), never resolvable after.
	ErrApprovalExpired = errors.New("pending approval has expired")
)
