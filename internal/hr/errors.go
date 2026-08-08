// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package hr

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm at
	// all - same convention as every other package's ErrFirmNotFound
	// (internal/permission, internal/accounting, ...).
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - people/contract writes are
	// owner-gated (see this package's doc comment).
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's people or contracts")

	// ErrPersonNotFound means no people row with the given id is visible
	// in the caller's firm context.
	ErrPersonNotFound = errors.New("person not found")

	// ErrInvalidPerson means CreatePerson/UpdatePerson was given a
	// structurally invalid person (empty name, unknown type/status).
	ErrInvalidPerson = errors.New("invalid person")

	// ErrInvalidContract means CreateContract was given a structurally
	// invalid contract (empty type, no start date).
	ErrInvalidContract = errors.New("invalid contract")
)
