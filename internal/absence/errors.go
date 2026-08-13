// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package absence

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm at
	// all.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - approve/reject are owner-gated per
	// this batch's task spec.
	ErrNotOwner = errors.New("caller is not authorized to approve or reject this firm's absences")

	// ErrAbsenceNotFound means no absences row with the given id is
	// visible in the caller's firm context.
	ErrAbsenceNotFound = errors.New("absence not found")

	// ErrInvalidAbsence means CreateAbsence/UpdateAbsence was given a
	// structurally invalid absence (unknown type, end before start,
	// unknown person).
	ErrInvalidAbsence = errors.New("invalid absence")

	// ErrNotAbsenceOwner means the caller is a real member of the firm but
	// is neither the absence's own requester nor an owner - update/delete
	// while pending is restricted the same way
	// internal/timetracking.ErrNotEntryOwner restricts time entries.
	ErrNotAbsenceOwner = errors.New("caller may only edit or delete their own absence requests")

	// ErrAbsenceNotPending means UpdateAbsence/DeleteAbsence/Approve/
	// Reject was attempted against an absence whose status is no longer
	// 'pending' - resolved absences are immutable history.
	ErrAbsenceNotPending = errors.New("absence is not pending")

	// ErrNoPendingApproval means Approve/Reject was called on an absence
	// whose workflow_instance_id has no pending_approvals row to resolve -
	// either CreateAbsence's own post-commit RequestApproval call never
	// succeeded (see CreateAbsence's own doc comment for this documented
	// limitation) or the approval was already resolved through some other
	// path.
	ErrNoPendingApproval = errors.New("no pending approval found for this absence")
)
