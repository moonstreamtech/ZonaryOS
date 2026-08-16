// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package project

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - project writes are owner-gated,
	// same tier internal/crm's customer/opportunity mutations use.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's projects")

	// ErrProjectNotFound means no projects row with the given id is
	// visible in the caller's firm context.
	ErrProjectNotFound = errors.New("project not found")

	// ErrInvalidProject means CreateProject/UpdateProject was given a
	// structurally invalid project (empty name, malformed budget, unknown
	// status).
	ErrInvalidProject = errors.New("invalid project")
)
