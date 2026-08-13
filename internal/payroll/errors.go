// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package payroll

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm at
	// all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - payroll writes (periods/entries/
	// close) are owner-gated, the same tier as internal/hr's people/
	// contracts.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's payroll")

	// ErrPeriodNotFound means no payroll_periods row with the given id is
	// visible in the caller's firm context.
	ErrPeriodNotFound = errors.New("payroll period not found")

	// ErrEntryNotFound means no payroll_entries row with the given id is
	// visible in the caller's firm context/period.
	ErrEntryNotFound = errors.New("payroll entry not found")

	// ErrInvalidPeriod means CreatePeriod/UpdatePeriod was given a
	// structurally invalid period (empty name, end before start).
	ErrInvalidPeriod = errors.New("invalid payroll period")

	// ErrInvalidEntry means CreateEntry/UpdateEntry was given a
	// structurally invalid entry (bad amount, unknown person).
	ErrInvalidEntry = errors.New("invalid payroll entry")

	// ErrPeriodNotOpen means a payroll_entries write was attempted against
	// a period that is no longer 'open' - entries are immutable history
	// once a period starts closing.
	ErrPeriodNotOpen = errors.New("payroll period is not open")

	// ErrPeriodAlreadyClosed means ClosePeriod was called on a period
	// whose status is already 'closed' - closing is idempotency-guarded,
	// not a no-op success: a caller retrying a close should learn their
	// retry didn't double-post anything, not silently succeed twice.
	ErrPeriodAlreadyClosed = errors.New("payroll period is already closed")
)
