// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package manufacturing

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - BOM/production order writes are
	// owner-gated, same tier internal/inventory's product mutations use.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's manufacturing data")

	// ErrBOMNotFound means no bom_headers row with the given id is
	// visible in the caller's firm context.
	ErrBOMNotFound = errors.New("bill of materials not found")

	// ErrBOMLineNotFound means no bom_lines row with the given id is
	// visible on the given BOM.
	ErrBOMLineNotFound = errors.New("bill of materials line not found")

	// ErrInvalidBOM means CreateBOM/UpdateBOM was given a structurally
	// invalid BOM (empty name, no components).
	ErrInvalidBOM = errors.New("invalid bill of materials")

	// ErrInvalidBOMLine means AddBOMLine/UpdateBOMLine was given a
	// structurally invalid line (non-positive quantity).
	ErrInvalidBOMLine = errors.New("invalid bill of materials line")

	// ErrProductionOrderNotFound means no production_orders row with the
	// given id is visible in the caller's firm context.
	ErrProductionOrderNotFound = errors.New("production order not found")

	// ErrInvalidProductionOrder means CreateProductionOrder was given a
	// structurally invalid order (non-positive planned quantity, a BOM
	// that doesn't belong to the given product).
	ErrInvalidProductionOrder = errors.New("invalid production order")

	// ErrInvalidProductionOrderTransition means Start/Complete/Cancel was
	// called on a production order not in the required status for that
	// transition (e.g. completing an order that's still 'planned').
	ErrInvalidProductionOrderTransition = errors.New("invalid production order status transition")
)
