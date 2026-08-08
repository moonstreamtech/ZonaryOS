// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package logistics

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - delivery writes are owner-gated
	// (see this package's doc comment).
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's deliveries")

	// ErrDeliveryNotFound means no deliveries row with the given id is
	// visible in the caller's firm context.
	ErrDeliveryNotFound = errors.New("delivery not found")

	// ErrInvalidDelivery means CreateDelivery/UpdateDelivery/CreateDeliveryTx
	// was given a structurally invalid delivery (an unrecognized status).
	ErrInvalidDelivery = errors.New("invalid delivery")
)
