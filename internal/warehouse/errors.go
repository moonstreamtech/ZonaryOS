// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package warehouse

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - warehouse/location/transfer/cycle
	// count writes are owner-gated, same tier internal/inventory's own
	// product mutations use.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's warehouse data")

	// ErrWarehouseNotFound means no warehouses row with the given id is
	// visible in the caller's firm context.
	ErrWarehouseNotFound = errors.New("warehouse not found")

	// ErrInvalidWarehouse means CreateWarehouse/UpdateWarehouse was given
	// a structurally invalid warehouse (empty name).
	ErrInvalidWarehouse = errors.New("invalid warehouse")

	// ErrLocationNotFound means no warehouse_locations row with the given
	// id is visible on the given warehouse.
	ErrLocationNotFound = errors.New("warehouse location not found")

	// ErrInvalidLocation means CreateLocation/UpdateLocation was given a
	// structurally invalid location (empty code, unknown type, a parent
	// that doesn't belong to the same warehouse).
	ErrInvalidLocation = errors.New("invalid warehouse location")

	// ErrTransferNotFound means no inventory_transfers row with the given
	// id is visible in the caller's firm context.
	ErrTransferNotFound = errors.New("inventory transfer not found")

	// ErrInvalidTransfer means CreateTransfer was given a structurally
	// invalid transfer (non-positive quantity, identical from/to
	// locations, a location that doesn't belong to firmID).
	ErrInvalidTransfer = errors.New("invalid inventory transfer")

	// ErrInvalidTransferTransition means Complete/Cancel was called on a
	// transfer not in the required status for that transition.
	ErrInvalidTransferTransition = errors.New("invalid inventory transfer status transition")

	// ErrCycleCountSessionNotFound means no cycle_count_sessions row with
	// the given id is visible in the caller's firm context.
	ErrCycleCountSessionNotFound = errors.New("cycle count session not found")

	// ErrCycleCountLineNotFound means no cycle_count_lines row with the
	// given id is visible on the given session.
	ErrCycleCountLineNotFound = errors.New("cycle count line not found")

	// ErrInvalidCycleCount means CreateCycleCountSession/RecordCount was
	// given structurally invalid input (no lines, a malformed counted
	// quantity).
	ErrInvalidCycleCount = errors.New("invalid cycle count")

	// ErrInvalidCycleCountTransition means RecordCount/CompleteCycleCountSession
	// was called on a session not in the required status.
	ErrInvalidCycleCountTransition = errors.New("invalid cycle count session status transition")
)
