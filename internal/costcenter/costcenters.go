// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package costcenter is the cost center half of the budget management /
// cost centers / financial planning batch: a simple, optionally-nested
// (self-referencing parent_id) cost center catalog, plus GetCostCenterPnL
// - a P&L grouped by cost center instead of by account. journal_lines'
// new (nullable) cost_center_id column is populated by
// internal/workflow's own journal bridge when a bridged transition's
// payload carries a cost_center_id field - see engine.go's
// resolveJournalLines for that propagation; this package never writes to
// journal_lines directly, only reads.
//
// Authorization follows this codebase's usual discipline: IsMember
// before any read, IsMember then IsOwner before any write - the same
// tier internal/asset's own mutations use.
package costcenter

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
	costCenterAuditEntityType = "cost_center"

	createAuditAction = "create"
	updateAuditAction = "update"
)

// postgresUniqueViolation is the SQLSTATE code Postgres raises for a
// UNIQUE constraint violation - same constant/reasoning as every other
// package in this codebase that redeclares it locally.
const postgresUniqueViolation = "23505"

// CostCenter is one cost_centers row.
type CostCenter struct {
	ID        uuid.UUID
	FirmID    uuid.UUID
	Code      string
	Name      string
	ParentID  *uuid.UUID
	IsActive  bool
	CreatedAt time.Time
}

// CreateCostCenterInput is CreateCostCenter's request shape.
type CreateCostCenterInput struct {
	Code     string
	Name     string
	ParentID *uuid.UUID
}

// CreateCostCenter adds a new cost center to firmID. Owner-gated. If
// ParentID is set, it must name another cost center within the SAME
// firm - checked here in Go so a cross-firm parent comes back as a clean
// ErrInvalidCostCenter rather than relying on cost_centers' own FK
// (which only guarantees the parent row exists somewhere, not that it's
// in the same firm) - same reasoning internal/warehouse.CreateLocation's
// own cross-warehouse-parent check gives.
func CreateCostCenter(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateCostCenterInput) (CostCenter, error) {
	code := strings.TrimSpace(input.Code)
	if code == "" {
		return CostCenter{}, fmt.Errorf("%w: code must not be empty", ErrInvalidCostCenter)
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CostCenter{}, fmt.Errorf("%w: name must not be empty", ErrInvalidCostCenter)
	}

	var c CostCenter
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

		if input.ParentID != nil {
			exists, err := ExistsTx(ctx, tx, firmID, *input.ParentID)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%w: parent cost center does not belong to this firm", ErrInvalidCostCenter)
			}
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO cost_centers (firm_id, code, name, parent_id) VALUES ($1, $2, $3, $4)
			RETURNING `+costCenterColumns, firmID, code, name, input.ParentID)
		var scanErr error
		c, scanErr = scanCostCenterRow(row)
		if scanErr != nil {
			var pgErr *pgconn.PgError
			if errors.As(scanErr, &pgErr) && pgErr.Code == postgresUniqueViolation {
				return ErrDuplicateCode
			}
			return fmt.Errorf("insert cost center: %w", scanErr)
		}

		return auditlog.Write(ctx, tx, firmID, userID, c.ID, costCenterAuditEntityType, createAuditAction, map[string]any{"code": code, "name": name})
	})
	if err != nil {
		return CostCenter{}, err
	}
	return c, nil
}

// UpdateCostCenterInput is UpdateCostCenter's partial-update input - nil
// leaves that field unchanged, the same convention internal/asset's own
// UpdateAssetInput uses.
type UpdateCostCenterInput struct {
	Name     *string
	IsActive *bool
}

// UpdateCostCenter applies input's changes to costCenterID. Owner-gated.
func UpdateCostCenter(ctx context.Context, pool *pgxpool.Pool, firmID, userID, costCenterID uuid.UUID, input UpdateCostCenterInput) (CostCenter, error) {
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		return CostCenter{}, fmt.Errorf("%w: name must not be empty", ErrInvalidCostCenter)
	}

	var c CostCenter
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
			UPDATE cost_centers SET
				name = COALESCE($3, name),
				is_active = COALESCE($4, is_active)
			WHERE id = $1 AND firm_id = $2
			RETURNING `+costCenterColumns, costCenterID, firmID, input.Name, input.IsActive)
		var scanErr error
		c, scanErr = scanCostCenterRow(row)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ErrCostCenterNotFound
		}
		if scanErr != nil {
			return fmt.Errorf("update cost center: %w", scanErr)
		}

		return auditlog.Write(ctx, tx, firmID, userID, c.ID, costCenterAuditEntityType, updateAuditAction, map[string]any{})
	})
	if err != nil {
		return CostCenter{}, err
	}
	return c, nil
}

// ListCostCenters returns firmID's cost centers, ordered by code.
// Member-gated.
func ListCostCenters(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]CostCenter, error) {
	var centers []CostCenter
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+costCenterColumns+` FROM cost_centers WHERE firm_id = $1 ORDER BY code`, firmID)
		if err != nil {
			return fmt.Errorf("list cost centers: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCostCenterRow(rows)
			if err != nil {
				return err
			}
			centers = append(centers, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return centers, nil
}

// ExistsTx reports whether costCenterID exists within firmID - the
// existence-check primitive internal/workflow's costCenterValidator
// (FieldTypeCostCenter) calls, same shape as internal/asset.AssetExistsTx.
//
// ciaudit:ignore-firmid-check: read-only existence helper, no data
// returned beyond a boolean - every caller already established its own
// firm/permission context before calling this.
func ExistsTx(ctx context.Context, tx pgx.Tx, firmID, costCenterID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM cost_centers WHERE id = $1 AND firm_id = $2)
	`, costCenterID, firmID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check cost center exists: %w", err)
	}
	return exists, nil
}

const costCenterColumns = `id, firm_id, code, name, parent_id, is_active, created_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCostCenterRow(row rowScanner) (CostCenter, error) {
	var c CostCenter
	if err := row.Scan(&c.ID, &c.FirmID, &c.Code, &c.Name, &c.ParentID, &c.IsActive, &c.CreatedAt); err != nil {
		return CostCenter{}, err
	}
	return c, nil
}
