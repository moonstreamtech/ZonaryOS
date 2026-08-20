// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package alerting

import "errors"

var (
	// ErrInvalidRule is returned for a malformed alert_rules write - an
	// empty name, an unrecognized metric, a non-positive window_minutes.
	ErrInvalidRule = errors.New("invalid alert rule")
	// ErrRuleNotFound is returned when ruleID doesn't match any existing
	// alert_rules row.
	ErrRuleNotFound = errors.New("alert rule not found")
)
