// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package analytics

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm.
	ErrFirmNotFound = errors.New("firm not found")
	// ErrInvalidEventBatch means the ingested batch was empty or exceeded
	// the 100-event-per-request cap.
	ErrInvalidEventBatch = errors.New("invalid analytics event batch")
	// ErrInvalidQuery means a time-series query spec had an unsupported
	// group_by, or a malformed from/to date.
	ErrInvalidQuery = errors.New("invalid analytics query")
)
