// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package salesorders

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm at
	// all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - direct sales order writes are
	// owner-gated, the same tier internal/invoicing's CreateInvoice uses.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's sales orders")

	// ErrSalesOrderNotFound means no sales_orders row with the given id is
	// visible in the caller's firm context.
	ErrSalesOrderNotFound = errors.New("sales order not found")

	// ErrInvalidSalesOrder means CreateSalesOrder/UpdateSalesOrderStatus
	// was given a structurally invalid order (an unrecognized status, an
	// illegal status transition, no lines).
	ErrInvalidSalesOrder = errors.New("invalid sales order")

	// ErrInvalidSalesOrderLine means a requested line was structurally
	// invalid (empty description, non-positive quantity/unit price).
	ErrInvalidSalesOrderLine = errors.New("invalid sales order line")
)
