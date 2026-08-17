// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package costcenter

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - cost center writes are
	// owner-gated, same tier internal/asset's own mutations use.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's cost centers")

	// ErrCostCenterNotFound means no cost_centers row with the given id
	// is visible in the caller's firm context.
	ErrCostCenterNotFound = errors.New("cost center not found")

	// ErrInvalidCostCenter means CreateCostCenter/UpdateCostCenter was
	// given structurally invalid input (empty code/name, a parent that
	// doesn't belong to the same firm).
	ErrInvalidCostCenter = errors.New("invalid cost center")

	// ErrDuplicateCode means CreateCostCenter/UpdateCostCenter was given
	// a code already used by another cost center in the same firm
	// (cost_centers' own UNIQUE (firm_id, code) constraint).
	ErrDuplicateCode = errors.New("cost center code already in use")
)
