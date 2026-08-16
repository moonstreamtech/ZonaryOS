// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package inventory

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - product/supplier writes are
	// owner-gated (see this package's doc comment).
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's inventory or suppliers")

	// ErrProductNotFound means no products row with the given id/sku is
	// visible in the caller's firm context.
	ErrProductNotFound = errors.New("product not found")

	// ErrInvalidProduct means CreateProduct/UpdateProduct was given a
	// structurally invalid product (empty sku/name, malformed price).
	ErrInvalidProduct = errors.New("invalid product")

	// ErrProductSKUExists means firmID already has a products row with
	// the requested sku (UNIQUE (firm_id, sku)).
	ErrProductSKUExists = errors.New("a product with this SKU already exists for this firm")

	// ErrSupplierNotFound means no suppliers row with the given id is
	// visible in the caller's firm context.
	ErrSupplierNotFound = errors.New("supplier not found")

	// ErrInvalidSupplier means CreateSupplier/UpdateSupplier was given a
	// structurally invalid supplier (empty name).
	ErrInvalidSupplier = errors.New("invalid supplier")

	// ErrInsufficientStock means AdjustStockTx's quantityChange would
	// take stock_levels.quantity below zero for a product/location - the
	// design brief's "if quantity would go below 0, fail the transition
	// (not just a warning)". Backed by a real database CHECK constraint
	// too (migrations/0011_inventory_core.up.sql) - this error is the
	// clean, typed form of that same invariant, returned before the raw
	// constraint violation would otherwise surface.
	ErrInsufficientStock = errors.New("insufficient stock: this change would take quantity below zero")

	// ErrInvalidStockAdjustment means AdjustStockTx was given a
	// structurally invalid quantityChange (not a well-formed decimal) or
	// an empty reason.
	ErrInvalidStockAdjustment = errors.New("invalid stock adjustment")

	// ErrNoDefaultLocation means AdjustStockTx was called with a nil
	// locationID for a firm that has no warehouse_locations row flagged
	// is_default - shouldn't happen for any firm created through the
	// normal paths (internal/wizard.CreateDefaultFirm, or the
	// migrations/0028_warehouse_management.up.sql backfill for firms that
	// predate it), both of which unconditionally seed one.
	ErrNoDefaultLocation = errors.New("firm has no default warehouse location")
)
