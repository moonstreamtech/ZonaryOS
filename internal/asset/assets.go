// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package asset is the asset management / maintenance / facility
// operations module: an asset registry (equipment, vehicles, property,
// tools), maintenance scheduling with a GENERATED ALWAYS next_due_at
// column, maintenance records that update their schedule and optionally
// post a real journal entry, and a daily background scheduler that
// notifies the assigned person when maintenance is coming due. Not a full
// EAM/facilities suite: no depreciation calculation schedule
// (current_value/depreciation_rate are data fields only), no insurance
// tracking, no IoT sensor integration (Edge Agent, future), no QR/barcode
// asset tagging.
//
// Authorization follows this codebase's usual discipline: IsMember before
// any read, IsMember then IsOwner before any write - the same tier
// internal/warehouse's own mutations use.
package asset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const (
	assetAuditEntityType = "asset"

	createAuditAction = "create"
	updateAuditAction = "update"
)

// postgresUniqueViolation is the SQLSTATE code Postgres raises for a
// UNIQUE constraint violation - same constant/reasoning as every other
// package in this codebase that redeclares it locally.
const postgresUniqueViolation = "23505"

// assetTypes mirrors assets.type's own CHECK constraint
// (migrations/0030_asset_management.up.sql) - validated in Go too so a
// bad value comes back as a clean ErrInvalidAsset instead of a raw
// constraint-violation error, the same "app check first, DB constraint as
// defense in depth" pattern this codebase's other CHECK-backed columns
// already follow.
var assetTypes = map[string]bool{
	"equipment": true, "vehicle": true, "property": true, "tool": true, "other": true,
}

// assetStatuses mirrors assets.status's own CHECK constraint.
var assetStatuses = map[string]bool{
	"active": true, "maintenance": true, "retired": true, "disposed": true,
}

// Asset is one assets row.
type Asset struct {
	ID                 uuid.UUID
	FirmID             uuid.UUID
	Name               string
	AssetNumber        string
	Type               string
	Status             string
	LocationID         *uuid.UUID
	AssignedToPersonID *uuid.UUID
	PurchaseDate       *time.Time
	PurchasePrice      *string
	CurrentValue       *string
	DepreciationRate   *string
	SerialNumber       *string
	WarrantyExpires    *time.Time
	Notes              *string
	CustomFields       map[string]any
	CreatedAt          time.Time
}

// CreateAssetInput is CreateAsset's request shape. PurchaseDate/
// WarrantyExpires are plain "YYYY-MM-DD" strings (parsed via
// parseOptionalDate), the same convention internal/crm's own
// CreateOpportunityInput.ExpectedCloseDate uses - not RFC3339 timestamps,
// since these are DATE columns.
type CreateAssetInput struct {
	Name               string
	AssetNumber        string
	Type               string
	Status             string // optional, defaults to "active" (assets' own DB default)
	LocationID         *uuid.UUID
	AssignedToPersonID *uuid.UUID
	PurchaseDate       string
	PurchasePrice      *string
	CurrentValue       *string
	DepreciationRate   *string
	SerialNumber       string
	WarrantyExpires    string
	Notes              string
	CustomFields       map[string]any
}

func validateAssetFields(name, assetNumber, typ, status string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("%w: name must not be empty", ErrInvalidAsset)
	}
	if strings.TrimSpace(assetNumber) == "" {
		return "", fmt.Errorf("%w: asset number must not be empty", ErrInvalidAsset)
	}
	if !assetTypes[typ] {
		return "", fmt.Errorf("%w: type %q is not one of equipment/vehicle/property/tool/other", ErrInvalidAsset, typ)
	}
	if status != "" && !assetStatuses[status] {
		return "", fmt.Errorf("%w: status %q is not one of active/maintenance/retired/disposed", ErrInvalidAsset, status)
	}
	return name, nil
}

// CreateAsset adds a new asset to firmID. Owner-gated: registering a
// firm's physical assets is a structural firm decision, the same tier as
// internal/warehouse.CreateWarehouse.
func CreateAsset(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateAssetInput) (Asset, error) {
	name, err := validateAssetFields(input.Name, input.AssetNumber, input.Type, input.Status)
	if err != nil {
		return Asset{}, err
	}
	assetNumber := strings.TrimSpace(input.AssetNumber)
	status := input.Status
	if status == "" {
		status = "active"
	}
	if input.CustomFields == nil {
		input.CustomFields = map[string]any{}
	}
	purchaseDate, err := parseOptionalDate(input.PurchaseDate)
	if err != nil {
		return Asset{}, fmt.Errorf("%w: purchaseDate must be YYYY-MM-DD", ErrInvalidAsset)
	}
	warrantyExpires, err := parseOptionalDate(input.WarrantyExpires)
	if err != nil {
		return Asset{}, fmt.Errorf("%w: warrantyExpires must be YYYY-MM-DD", ErrInvalidAsset)
	}

	var a Asset
	err = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO assets (
				firm_id, name, asset_number, type, status, location_id, assigned_to_person_id,
				purchase_date, purchase_price, current_value, depreciation_rate, serial_number,
				warranty_expires, notes, custom_fields
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			RETURNING id, firm_id, name, asset_number, type, status, location_id, assigned_to_person_id,
				purchase_date, purchase_price::text, current_value::text, depreciation_rate::text,
				serial_number, warranty_expires, notes, custom_fields, created_at
		`, firmID, name, assetNumber, input.Type, status, input.LocationID, input.AssignedToPersonID,
			purchaseDate, input.PurchasePrice, input.CurrentValue, input.DepreciationRate,
			optionalString(input.SerialNumber), warrantyExpires, optionalString(input.Notes), input.CustomFields)
		var scanErr error
		a, scanErr = scanAssetRow(row)
		if scanErr != nil {
			var pgErr *pgconn.PgError
			if errors.As(scanErr, &pgErr) && pgErr.Code == postgresUniqueViolation {
				return ErrDuplicateAssetNumber
			}
			return fmt.Errorf("insert asset: %w", scanErr)
		}

		return auditlog.Write(ctx, tx, firmID, userID, a.ID, assetAuditEntityType, createAuditAction, map[string]any{"name": name, "assetNumber": assetNumber})
	})
	if err != nil {
		return Asset{}, err
	}
	return a, nil
}

// UpdateAssetInput is UpdateAsset's partial-update input - nil leaves that
// field unchanged, the same convention internal/inventory.ProductUpdate
// uses. Money/date fields use a "has value" wrapper via pointer-to-pointer
// only where clearing to NULL matters; here we only support setting them
// (not clearing), matching internal/warehouse.UpdateWarehouseInput's own
// scope boundary.
type UpdateAssetInput struct {
	Name               *string
	Status             *string
	LocationID         *uuid.UUID
	HasLocationID      bool
	AssignedToPersonID *uuid.UUID
	HasAssignedTo      bool
	PurchaseDate       *string
	PurchasePrice      *string
	CurrentValue       *string
	DepreciationRate   *string
	SerialNumber       *string
	WarrantyExpires    *string
	Notes              *string
	CustomFields       map[string]any
}

// UpdateAsset applies input's changes to assetID. Owner-gated.
func UpdateAsset(ctx context.Context, pool *pgxpool.Pool, firmID, userID, assetID uuid.UUID, input UpdateAssetInput) (Asset, error) {
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		return Asset{}, fmt.Errorf("%w: name must not be empty", ErrInvalidAsset)
	}
	if input.Status != nil && !assetStatuses[*input.Status] {
		return Asset{}, fmt.Errorf("%w: status %q is not one of active/maintenance/retired/disposed", ErrInvalidAsset, *input.Status)
	}
	var purchaseDate, warrantyExpires *time.Time
	if input.PurchaseDate != nil {
		d, err := parseOptionalDate(*input.PurchaseDate)
		if err != nil {
			return Asset{}, fmt.Errorf("%w: purchaseDate must be YYYY-MM-DD", ErrInvalidAsset)
		}
		purchaseDate = d
	}
	if input.WarrantyExpires != nil {
		d, err := parseOptionalDate(*input.WarrantyExpires)
		if err != nil {
			return Asset{}, fmt.Errorf("%w: warrantyExpires must be YYYY-MM-DD", ErrInvalidAsset)
		}
		warrantyExpires = d
	}

	var a Asset
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		row := tx.QueryRow(ctx, `
			UPDATE assets SET
				name = COALESCE($3, name),
				status = COALESCE($4, status),
				location_id = CASE WHEN $5::boolean THEN $6 ELSE location_id END,
				assigned_to_person_id = CASE WHEN $7::boolean THEN $8 ELSE assigned_to_person_id END,
				purchase_date = COALESCE($9, purchase_date),
				purchase_price = COALESCE($10, purchase_price),
				current_value = COALESCE($11, current_value),
				depreciation_rate = COALESCE($12, depreciation_rate),
				serial_number = COALESCE($13, serial_number),
				warranty_expires = COALESCE($14, warranty_expires),
				notes = COALESCE($15, notes),
				custom_fields = COALESCE($16, custom_fields)
			WHERE id = $1 AND firm_id = $2
			RETURNING id, firm_id, name, asset_number, type, status, location_id, assigned_to_person_id,
				purchase_date, purchase_price::text, current_value::text, depreciation_rate::text,
				serial_number, warranty_expires, notes, custom_fields, created_at
		`, assetID, firmID, input.Name, input.Status, input.HasLocationID, input.LocationID,
			input.HasAssignedTo, input.AssignedToPersonID, purchaseDate, input.PurchasePrice,
			input.CurrentValue, input.DepreciationRate, input.SerialNumber, warrantyExpires,
			input.Notes, input.CustomFields)
		var scanErr error
		a, scanErr = scanAssetRow(row)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ErrAssetNotFound
		}
		if scanErr != nil {
			var pgErr *pgconn.PgError
			if errors.As(scanErr, &pgErr) && pgErr.Code == postgresUniqueViolation {
				return ErrDuplicateAssetNumber
			}
			return fmt.Errorf("update asset: %w", scanErr)
		}

		return auditlog.Write(ctx, tx, firmID, userID, a.ID, assetAuditEntityType, updateAuditAction, map[string]any{})
	})
	if err != nil {
		return Asset{}, err
	}
	return a, nil
}

// ListAssets returns firmID's assets, ordered by name. Member-gated.
func ListAssets(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Asset, error) {
	var assets []Asset
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, name, asset_number, type, status, location_id, assigned_to_person_id,
				purchase_date, purchase_price::text, current_value::text, depreciation_rate::text,
				serial_number, warranty_expires, notes, custom_fields, created_at
			FROM assets WHERE firm_id = $1 ORDER BY name
		`, firmID)
		if err != nil {
			return fmt.Errorf("list assets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanAssetRow(rows)
			if err != nil {
				return err
			}
			assets = append(assets, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return assets, nil
}

// GetAsset resolves one asset by id within firmID. Member-gated.
func GetAsset(ctx context.Context, pool *pgxpool.Pool, firmID, userID, assetID uuid.UUID) (Asset, error) {
	var a Asset
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `
			SELECT id, firm_id, name, asset_number, type, status, location_id, assigned_to_person_id,
				purchase_date, purchase_price::text, current_value::text, depreciation_rate::text,
				serial_number, warranty_expires, notes, custom_fields, created_at
			FROM assets WHERE id = $1 AND firm_id = $2
		`, assetID, firmID)
		var scanErr error
		a, scanErr = scanAssetRow(row)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ErrAssetNotFound
		}
		return scanErr
	})
	if err != nil {
		return Asset{}, err
	}
	return a, nil
}

// AssetExistsTx reports whether assetID exists within firmID - the
// existence-check primitive internal/workflow's assetValidator
// (FieldTypeAsset) calls, same shape as internal/hr.PersonExistsTx/
// internal/project.ProjectExistsTx.
//
// ciaudit:ignore-firmid-check: read-only existence helper, no data
// returned beyond a boolean - every caller already established its own
// firm/permission context before calling this.
func AssetExistsTx(ctx context.Context, tx pgx.Tx, firmID, assetID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM assets WHERE id = $1 AND firm_id = $2)
	`, assetID, firmID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check asset exists: %w", err)
	}
	return exists, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAssetRow(row rowScanner) (Asset, error) {
	var a Asset
	if err := row.Scan(
		&a.ID, &a.FirmID, &a.Name, &a.AssetNumber, &a.Type, &a.Status, &a.LocationID, &a.AssignedToPersonID,
		&a.PurchaseDate, &a.PurchasePrice, &a.CurrentValue, &a.DepreciationRate,
		&a.SerialNumber, &a.WarrantyExpires, &a.Notes, &a.CustomFields, &a.CreatedAt,
	); err != nil {
		return Asset{}, err
	}
	return a, nil
}

// optionalString returns nil for an empty/whitespace-only string, else a
// pointer to the trimmed value - same helper internal/warehouse's own
// package defines locally.
func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// parseOptionalDate parses a plain "YYYY-MM-DD" string into a *time.Time,
// or returns nil for an empty string - same helper/convention
// internal/crm's own parseOptionalDate uses for DATE-column fields.
func parseOptionalDate(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
