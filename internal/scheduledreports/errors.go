// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package scheduledreports

import "errors"

var (
	ErrFirmNotFound            = errors.New("firm not found")
	ErrNotOwner                = errors.New("not an owner")
	ErrInvalidScheduledReport  = errors.New("invalid scheduled report")
	ErrScheduledReportNotFound = errors.New("scheduled report not found")
)
