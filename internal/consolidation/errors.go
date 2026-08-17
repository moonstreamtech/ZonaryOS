// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package consolidation

import "errors"

// ErrNotPlatformAdmin is NOT redeclared here - every function in this
// package returns internal/platformadmin.ErrNotPlatformAdmin directly
// (the same allowlist, the same sentinel), matching internal/currency's
// own reuse of that type rather than each platform-admin-gated package
// growing its own copy.
var (
	ErrInvalidGroup        = errors.New("invalid firm group input")
	ErrGroupNotFound       = errors.New("firm group not found")
	ErrFirmNotFound        = errors.New("firm not found")
	ErrFirmNotInGroup      = errors.New("firm is not a member of this group")
	ErrDuplicateMembership = errors.New("firm is already a member of this group")
	ErrInvalidTransfer     = errors.New("invalid intercompany transfer input")
)
