// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package portability

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - export/import are owner-gated
	// (see this package's doc comment).
	ErrNotOwner = errors.New("caller is not authorized to export or import this firm's data")

	// ErrInvalidExportDocument means ImportConfiguration was given a
	// document that isn't a well-formed export (unreadable JSON, or a
	// version this package doesn't know how to import).
	ErrInvalidExportDocument = errors.New("invalid export document")
)
