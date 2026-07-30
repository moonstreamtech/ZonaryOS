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
)
