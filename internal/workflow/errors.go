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
)
