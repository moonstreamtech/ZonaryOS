// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package procurement

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm at
	// all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - direct purchase order writes are
	// owner-gated, same tier internal/salesorders uses for sales orders.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's purchase orders")

	// ErrPurchaseOrderNotFound means no purchase_orders row with the given
	// id is visible in the caller's firm context.
	ErrPurchaseOrderNotFound = errors.New("purchase order not found")

	// ErrInvalidPurchaseOrder means CreatePurchaseOrder/UpdatePurchaseOrderStatus
	// was given a structurally invalid order (an unrecognized status, an
	// illegal status transition, no lines).
	ErrInvalidPurchaseOrder = errors.New("invalid purchase order")

	// ErrInvalidPurchaseOrderLine means a requested line was structurally
	// invalid (empty description, non-positive quantity/unit price).
	ErrInvalidPurchaseOrderLine = errors.New("invalid purchase order line")
)
