// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package manufacturing is the manufacturing module foundation: a Bill of
// Materials (BOM) per product, and production orders that consume a BOM's
// components and produce finished goods - the data model and operations
// foundation Vision §3 names as a core domain, not a full MES (no MRP, no
// capacity planning, no machine/workstation routing, no batch/lot
// tracking).
//
// Authorization follows this codebase's usual discipline: IsMember before
// any read, IsMember then IsOwner before any write - the same tier
// internal/inventory's product/supplier mutations use.
package manufacturing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const bomAuditEntityType = "bom"

const (
	createBOMAuditAction = "create"
	updateBOMAuditAction = "update"
	deleteBOMAuditAction = "delete"
)

// quantityPattern mirrors internal/inventory's own amountPattern shape,
// but numeric(10,4) (bom_headers.unit_yield/bom_lines.quantity_per_unit),
// not numeric(19,4) - redeclared here rather than imported cross-package,
// same reasoning every other package's own copy of this pattern gives.
var quantityPattern = regexp.MustCompile(`^[0-9]{1,6}(\.[0-9]{1,4})?$`)

func validPositiveQuantity(s string) bool {
	return quantityPattern.MatchString(strings.TrimSpace(s))
}

// BOMHeader is one bom_headers row, with its component lines.
type BOMHeader struct {
	ID        uuid.UUID
	FirmID    uuid.UUID
	ProductID uuid.UUID
	Name      string
	Version   string
	IsActive  bool
	UnitYield string
	Notes     *string
	CreatedAt time.Time
	Lines     []BOMLine
}

// BOMLine is one bom_lines row.
type BOMLine struct {
	ID                 uuid.UUID
	BOMID              uuid.UUID
	ComponentProductID uuid.UUID
	QuantityPerUnit    string
	Unit               string
	Notes              *string
}

// BOMLineInput is one requested component line, on CreateBOMInput/AddBOMLine.
type BOMLineInput struct {
	ComponentProductID uuid.UUID
	QuantityPerUnit    string // decimal string
	Unit               string // optional, defaults to "piece"
	Notes              string
}

func validateLineInput(l BOMLineInput) error {
	if l.ComponentProductID == uuid.Nil {
		return fmt.Errorf("%w: component product id must not be empty", ErrInvalidBOMLine)
	}
	if !validPositiveQuantity(l.QuantityPerUnit) {
		return fmt.Errorf("%w: quantityPerUnit %q must be a positive decimal with at most 4 fraction digits", ErrInvalidBOMLine, l.QuantityPerUnit)
	}
	return nil
}

// deactivateOtherActiveBOMsTx clears is_active on every OTHER bom_headers
// row for (firmID, productID, excludeBOMID) - the enforcement half of "a
// product can have multiple BOM versions but only one active at a time"
// (this batch's design brief). Called from inside the same transaction
// that sets the new/updated BOM's own is_active to true, so the old
// active version and the new one flip together atomically - there is no
// window where a caller could observe two active versions, or zero.
//
// ciaudit:ignore-firmid-check: internal helper, only called by
// insertBOMTx/setBOMActiveTx, both of which have already run their own
// permission.IsOwner check before calling this.
func deactivateOtherActiveBOMsTx(ctx context.Context, tx pgx.Tx, firmID, productID, excludeBOMID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		UPDATE bom_headers SET is_active = false
		WHERE firm_id = $1 AND product_id = $2 AND id != $3 AND is_active = true
	`, firmID, productID, excludeBOMID); err != nil {
		return fmt.Errorf("deactivate other active BOM versions: %w", err)
	}
	return nil
}

// CreateBOMInput is CreateBOM's request shape.
type CreateBOMInput struct {
	ProductID uuid.UUID
	Name      string
	Version   string // optional, defaults to "1.0" (bom_headers' own DB default)
	IsActive  *bool  // optional, defaults to true (bom_headers' own DB default)
	UnitYield string // optional decimal string, defaults to "1"
	Notes     string
	Lines     []BOMLineInput
}

// CreateBOM adds a new BOM (with its component lines) to firmID -
// owner-gated. If IsActive is true (the default when unset, matching
// bom_headers.is_active's own DB default), every other BOM version for
// the same product is deactivated in the SAME transaction
// (deactivateOtherActiveBOMsTx) - so "only one active version per
// product" holds immediately, not just eventually.
func CreateBOM(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateBOMInput) (BOMHeader, error) {
	if strings.TrimSpace(input.Name) == "" {
		return BOMHeader{}, fmt.Errorf("%w: name must not be empty", ErrInvalidBOM)
	}
	if len(input.Lines) == 0 {
		return BOMHeader{}, fmt.Errorf("%w: a BOM must have at least one component line", ErrInvalidBOM)
	}
	for _, l := range input.Lines {
		if err := validateLineInput(l); err != nil {
			return BOMHeader{}, err
		}
	}
	version := strings.TrimSpace(input.Version)
	if version == "" {
		version = "1.0"
	}
	unitYield := strings.TrimSpace(input.UnitYield)
	if unitYield == "" {
		unitYield = "1"
	} else if !validPositiveQuantity(unitYield) {
		return BOMHeader{}, fmt.Errorf("%w: unitYield %q must be a positive decimal with at most 4 fraction digits", ErrInvalidBOM, unitYield)
	}
	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	var bom BOMHeader
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

		bom.FirmID = firmID
		bom.ProductID = input.ProductID
		bom.Name = strings.TrimSpace(input.Name)
		bom.Version = version
		bom.IsActive = isActive
		bom.UnitYield = unitYield
		if strings.TrimSpace(input.Notes) != "" {
			notes := strings.TrimSpace(input.Notes)
			bom.Notes = &notes
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO bom_headers (firm_id, product_id, name, version, is_active, unit_yield, notes)
			VALUES ($1, $2, $3, $4, $5, $6::numeric, $7)
			RETURNING id, created_at
		`, firmID, input.ProductID, bom.Name, version, isActive, unitYield, bom.Notes).Scan(&bom.ID, &bom.CreatedAt); err != nil {
			return fmt.Errorf("insert BOM: %w", err)
		}

		if isActive {
			if err := deactivateOtherActiveBOMsTx(ctx, tx, firmID, input.ProductID, bom.ID); err != nil {
				return err
			}
		}

		for _, l := range input.Lines {
			unit := strings.TrimSpace(l.Unit)
			if unit == "" {
				unit = "piece"
			}
			var notes *string
			if strings.TrimSpace(l.Notes) != "" {
				n := strings.TrimSpace(l.Notes)
				notes = &n
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO bom_lines (firm_id, bom_id, component_product_id, quantity_per_unit, unit, notes)
				VALUES ($1, $2, $3, $4::numeric, $5, $6)
			`, firmID, bom.ID, l.ComponentProductID, l.QuantityPerUnit, unit, notes); err != nil {
				return fmt.Errorf("insert BOM line: %w", err)
			}
		}

		bom.Lines, err = fetchBOMLinesTx(ctx, tx, bom.ID)
		if err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, bom.ID, bomAuditEntityType, createBOMAuditAction, map[string]any{
			"productId": bom.ProductID, "version": bom.Version,
		})
	})
	if err != nil {
		return BOMHeader{}, err
	}
	return bom, nil
}

func fetchBOMLinesTx(ctx context.Context, tx pgx.Tx, bomID uuid.UUID) ([]BOMLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, bom_id, component_product_id, quantity_per_unit::text, unit, notes
		FROM bom_lines WHERE bom_id = $1 ORDER BY id
	`, bomID)
	if err != nil {
		return nil, fmt.Errorf("list BOM lines: %w", err)
	}
	defer rows.Close()
	var lines []BOMLine
	for rows.Next() {
		var l BOMLine
		if err := rows.Scan(&l.ID, &l.BOMID, &l.ComponentProductID, &l.QuantityPerUnit, &l.Unit, &l.Notes); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

func scanBOMRow(row pgx.Row) (BOMHeader, error) {
	var b BOMHeader
	if err := row.Scan(&b.ID, &b.FirmID, &b.ProductID, &b.Name, &b.Version, &b.IsActive, &b.UnitYield, &b.Notes, &b.CreatedAt); err != nil {
		return BOMHeader{}, err
	}
	return b, nil
}

const bomColumns = `id, firm_id, product_id, name, version, is_active, unit_yield::text, notes, created_at`

// ListBOMs returns firmID's BOMs (without their lines - callers needing
// component detail use GetBOM), optionally filtered to one product,
// newest first. Member-gated.
func ListBOMs(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, productID *uuid.UUID) ([]BOMHeader, error) {
	var boms []BOMHeader
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		query := `SELECT ` + bomColumns + ` FROM bom_headers`
		args := []any{}
		if productID != nil {
			args = append(args, *productID)
			query += ` WHERE product_id = $1`
		}
		query += ` ORDER BY created_at DESC`

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list BOMs: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBOMRow(rows)
			if err != nil {
				return err
			}
			boms = append(boms, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return boms, nil
}

// GetBOM resolves one BOM (with its component lines) by id within
// firmID. Member-gated.
func GetBOM(ctx context.Context, pool *pgxpool.Pool, firmID, userID, bomID uuid.UUID) (BOMHeader, error) {
	var b BOMHeader
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+bomColumns+` FROM bom_headers WHERE id = $1 AND firm_id = $2`, bomID, firmID)
		b, err = scanBOMRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBOMNotFound
		}
		if err != nil {
			return fmt.Errorf("look up BOM: %w", err)
		}

		b.Lines, err = fetchBOMLinesTx(ctx, tx, bomID)
		return err
	})
	if err != nil {
		return BOMHeader{}, err
	}
	return b, nil
}

// UpdateBOMInput is UpdateBOM's request shape - every field OPTIONAL
// (nil pointer / empty string means "leave unchanged"), the same
// "PATCH-shaped" convention internal/inventory.UpdateProduct uses.
type UpdateBOMInput struct {
	Name     string
	Notes    *string
	IsActive *bool
}

// UpdateBOM applies input's changes to bomID. If IsActive is set to true,
// this deactivates every other BOM version for the same product in the
// SAME transaction (the version-management half of this batch's design
// brief: "when a BOM is set active, deactivate others for the same
// product"). Owner-gated.
func UpdateBOM(ctx context.Context, pool *pgxpool.Pool, firmID, userID, bomID uuid.UUID, input UpdateBOMInput) (BOMHeader, error) {
	var b BOMHeader
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

		var productID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT product_id FROM bom_headers WHERE id = $1 AND firm_id = $2`, bomID, firmID).Scan(&productID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrBOMNotFound
			}
			return fmt.Errorf("look up BOM: %w", err)
		}

		if strings.TrimSpace(input.Name) != "" {
			if _, err := tx.Exec(ctx, `UPDATE bom_headers SET name = $1 WHERE id = $2 AND firm_id = $3`, strings.TrimSpace(input.Name), bomID, firmID); err != nil {
				return fmt.Errorf("update BOM name: %w", err)
			}
		}
		if input.Notes != nil {
			if _, err := tx.Exec(ctx, `UPDATE bom_headers SET notes = NULLIF($1, '') WHERE id = $2 AND firm_id = $3`, *input.Notes, bomID, firmID); err != nil {
				return fmt.Errorf("update BOM notes: %w", err)
			}
		}
		if input.IsActive != nil {
			if _, err := tx.Exec(ctx, `UPDATE bom_headers SET is_active = $1 WHERE id = $2 AND firm_id = $3`, *input.IsActive, bomID, firmID); err != nil {
				return fmt.Errorf("update BOM active flag: %w", err)
			}
			if *input.IsActive {
				if err := deactivateOtherActiveBOMsTx(ctx, tx, firmID, productID, bomID); err != nil {
					return err
				}
			}
		}

		row := tx.QueryRow(ctx, `SELECT `+bomColumns+` FROM bom_headers WHERE id = $1 AND firm_id = $2`, bomID, firmID)
		b, err = scanBOMRow(row)
		if err != nil {
			return fmt.Errorf("re-read BOM: %w", err)
		}
		b.Lines, err = fetchBOMLinesTx(ctx, tx, bomID)
		if err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, bomID, bomAuditEntityType, updateBOMAuditAction, map[string]any{
			"isActive": b.IsActive,
		})
	})
	if err != nil {
		return BOMHeader{}, err
	}
	return b, nil
}

// DeleteBOM removes bomID (and its lines, via ON DELETE CASCADE) from
// firmID. Owner-gated.
func DeleteBOM(ctx context.Context, pool *pgxpool.Pool, firmID, userID, bomID uuid.UUID) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
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

		tag, err := tx.Exec(ctx, `DELETE FROM bom_headers WHERE id = $1 AND firm_id = $2`, bomID, firmID)
		if err != nil {
			return fmt.Errorf("delete BOM: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrBOMNotFound
		}

		return auditlog.Write(ctx, tx, firmID, userID, bomID, bomAuditEntityType, deleteBOMAuditAction, map[string]any{})
	})
}

// AddBOMLine appends a new component line to bomID. Owner-gated.
func AddBOMLine(ctx context.Context, pool *pgxpool.Pool, firmID, userID, bomID uuid.UUID, input BOMLineInput) (BOMHeader, error) {
	if err := validateLineInput(input); err != nil {
		return BOMHeader{}, err
	}
	var b BOMHeader
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

		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bom_headers WHERE id = $1 AND firm_id = $2)`, bomID, firmID).Scan(&exists); err != nil {
			return fmt.Errorf("look up BOM: %w", err)
		}
		if !exists {
			return ErrBOMNotFound
		}

		unit := strings.TrimSpace(input.Unit)
		if unit == "" {
			unit = "piece"
		}
		var notes *string
		if strings.TrimSpace(input.Notes) != "" {
			n := strings.TrimSpace(input.Notes)
			notes = &n
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO bom_lines (firm_id, bom_id, component_product_id, quantity_per_unit, unit, notes)
			VALUES ($1, $2, $3, $4::numeric, $5, $6)
		`, firmID, bomID, input.ComponentProductID, input.QuantityPerUnit, unit, notes); err != nil {
			return fmt.Errorf("insert BOM line: %w", err)
		}

		row := tx.QueryRow(ctx, `SELECT `+bomColumns+` FROM bom_headers WHERE id = $1 AND firm_id = $2`, bomID, firmID)
		b, err = scanBOMRow(row)
		if err != nil {
			return fmt.Errorf("re-read BOM: %w", err)
		}
		b.Lines, err = fetchBOMLinesTx(ctx, tx, bomID)
		return err
	})
	if err != nil {
		return BOMHeader{}, err
	}
	return b, nil
}

// DeleteBOMLine removes lineID from bomID. Owner-gated.
func DeleteBOMLine(ctx context.Context, pool *pgxpool.Pool, firmID, userID, bomID, lineID uuid.UUID) (BOMHeader, error) {
	var b BOMHeader
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

		tag, err := tx.Exec(ctx, `DELETE FROM bom_lines WHERE id = $1 AND bom_id = $2 AND firm_id = $3`, lineID, bomID, firmID)
		if err != nil {
			return fmt.Errorf("delete BOM line: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrBOMLineNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+bomColumns+` FROM bom_headers WHERE id = $1 AND firm_id = $2`, bomID, firmID)
		b, err = scanBOMRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBOMNotFound
		}
		if err != nil {
			return fmt.Errorf("re-read BOM: %w", err)
		}
		b.Lines, err = fetchBOMLinesTx(ctx, tx, bomID)
		return err
	})
	if err != nil {
		return BOMHeader{}, err
	}
	return b, nil
}
