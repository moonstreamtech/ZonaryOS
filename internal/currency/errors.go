// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package currency

import "errors"

var (
	// ErrRateNotFound means no exchange_rates row covers the requested
	// (from, to) pair at the requested instant - the caller (this
	// package's own ConvertAmount, or internal/accounting's journal
	// bridge) decides what to do about it; GetRate itself never
	// substitutes a fallback rate.
	ErrRateNotFound = errors.New("exchange rate not found")

	// ErrInvalidRate means CreateRate was given a structurally invalid
	// rate (empty currency code, non-positive rate, missing validFrom).
	ErrInvalidRate = errors.New("invalid exchange rate")
)
