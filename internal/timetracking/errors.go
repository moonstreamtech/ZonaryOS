// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package timetracking

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm at
	// all.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrEntryNotFound means no time_entries row with the given id is
	// visible in the caller's firm context.
	ErrEntryNotFound = errors.New("time entry not found")

	// ErrInvalidEntry means CreateEntry/UpdateEntry was given a
	// structurally invalid entry (bad date, hours out of range, unknown
	// person).
	ErrInvalidEntry = errors.New("invalid time entry")

	// ErrNotEntryOwner means the caller is a real member of the firm but
	// is neither the entry's own creator nor an owner - see this
	// package's own doc comment for why "own entries + owner override" is
	// the edit-restriction rule.
	ErrNotEntryOwner = errors.New("caller may only edit or delete their own time entries")
)
