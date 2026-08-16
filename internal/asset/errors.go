// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package asset

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - asset/maintenance writes are
	// owner-gated, same tier internal/warehouse's own mutations use.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's asset data")

	// ErrAssetNotFound means no assets row with the given id is visible
	// in the caller's firm context.
	ErrAssetNotFound = errors.New("asset not found")

	// ErrInvalidAsset means CreateAsset/UpdateAsset was given a
	// structurally invalid asset (empty name/asset number, unknown type).
	ErrInvalidAsset = errors.New("invalid asset")

	// ErrDuplicateAssetNumber means CreateAsset/UpdateAsset was given an
	// asset_number already used by another asset in the same firm
	// (assets' own UNIQUE (firm_id, asset_number) constraint).
	ErrDuplicateAssetNumber = errors.New("asset number already in use")

	// ErrMaintenanceScheduleNotFound means no maintenance_schedules row
	// with the given id is visible in the caller's firm context.
	ErrMaintenanceScheduleNotFound = errors.New("maintenance schedule not found")

	// ErrInvalidMaintenanceSchedule means CreateMaintenanceSchedule/
	// UpdateMaintenanceSchedule was given structurally invalid input
	// (empty name, non-positive interval_days).
	ErrInvalidMaintenanceSchedule = errors.New("invalid maintenance schedule")

	// ErrInvalidMaintenanceRecord means CreateMaintenanceRecord was given
	// structurally invalid input (empty description, a schedule_id that
	// doesn't belong to the same asset).
	ErrInvalidMaintenanceRecord = errors.New("invalid maintenance record")
)
