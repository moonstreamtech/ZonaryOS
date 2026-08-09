// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package notification

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotificationNotFound means no notifications row with the given ID
	// is visible to the caller - either it doesn't exist, it belongs to a
	// different firm, or (crucially) it belongs to a different recipient
	// within the same firm. All three resolve to the same error: a
	// notification is a personal inbox item, never partially visible.
	ErrNotificationNotFound = errors.New("notification not found")
)
