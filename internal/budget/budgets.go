// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package budget is the budget management half of the budget management/
// cost centers/financial planning batch: budgets (draft -> approved ->
// active -> closed) and their budget_lines (one planned amount per
// account). Not a full FP&A suite: single currency per budget (no
// multi-currency budget), no budget revisions/versions (create a new
// budget instead of versioning an old one), no rolling forecast, no cash
// flow forecasting.
//
// Authorization follows this codebase's usual discipline: IsMember
// before any read, IsMember then IsOwner before any write - the same
// tier internal/asset's own mutations use.
package budget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const (
	budgetAuditEntityType = "budget"

	createAuditAction  = "create"
	updateAuditAction  = "update"
	approveAuditAction = "approve"
)

// postgresUniqueViolation is the SQLSTATE code Postgres raises for a
// UNIQUE constraint violation - same constant/reasoning as every other
// package in this codebase that redeclares it locally.
const postgresUniqueViolation = "23505"

var periodTypes = map[string]bool{"monthly": true, "quarterly": true, "annual": true}
var budgetStatuses = map[string]bool{"draft": true, "approved": true, "active": true, "closed": true}

// Budget is one budgets row.
type Budget struct {
	ID          uuid.UUID
	FirmID      uuid.UUID
	Name        string
	PeriodType  string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Status      string
	Currency    string
	Notes       *string
	ApprovedBy  *uuid.UUID
	ApprovedAt  *time.Time
	CreatedAt   time.Time
}

// CreateBudgetInput is CreateBudget's request shape. PeriodStart/
// PeriodEnd are plain "YYYY-MM-DD" strings (parsed via parseDate), same
// convention internal/asset's own CreateAssetInput uses for DATE-column
// fields.
type CreateBudgetInput struct {
	Name        string
	PeriodType  string
	PeriodStart string
	PeriodEnd   string
	Status      string // optional, defaults to "draft" (budgets' own DB default)
	Currency    string
	Notes       string
}

func parseDate(s string) (time.Time, error) {
	return time.Parse("2006-01-02", strings.TrimSpace(s))
}

func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// CreateBudget adds a new budget to firmID. Owner-gated. If Status is
// explicitly "active", every other active budget for the firm whose
// period overlaps this one is deactivated back to "approved" in the same
// transaction (deactivateOtherActiveBudgetsTx) - "only one active budget
// per period," the exact same application-level "insert/update the new
// row, then flip every other conflicting row in one transaction" pattern
// internal/manufacturing's deactivateOtherActiveBOMsTx uses for BOM
// versions, not a DB-level partial unique index (a period-overlap
// exclusion constraint would need Postgres's btree_gist extension for a
// date-range GIST exclusion constraint - a heavier infrastructure
// dependency than this batch's brief asks for, when the BOM precedent
// already established the "application layer, same transaction" answer
// is sufficient).
func CreateBudget(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateBudgetInput) (Budget, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Budget{}, fmt.Errorf("%w: name must not be empty", ErrInvalidBudget)
	}
	if !periodTypes[input.PeriodType] {
		return Budget{}, fmt.Errorf("%w: period_type %q is not one of monthly/quarterly/annual", ErrInvalidBudget, input.PeriodType)
	}
	periodStart, err := parseDate(input.PeriodStart)
	if err != nil {
		return Budget{}, fmt.Errorf("%w: period_start must be YYYY-MM-DD", ErrInvalidBudget)
	}
	periodEnd, err := parseDate(input.PeriodEnd)
	if err != nil {
		return Budget{}, fmt.Errorf("%w: period_end must be YYYY-MM-DD", ErrInvalidBudget)
	}
	if periodEnd.Before(periodStart) {
		return Budget{}, fmt.Errorf("%w: period_end must not be before period_start", ErrInvalidBudget)
	}
	status := input.Status
	if status == "" {
		status = "draft"
	} else if !budgetStatuses[status] {
		return Budget{}, fmt.Errorf("%w: status %q is not one of draft/approved/active/closed", ErrInvalidBudget, status)
	}
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = "TRY"
	}

	var b Budget
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
			INSERT INTO budgets (firm_id, name, period_type, period_start, period_end, status, currency, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING `+budgetColumns, firmID, name, input.PeriodType, periodStart, periodEnd, status, currency, optionalString(input.Notes))
		var scanErr error
		b, scanErr = scanBudgetRow(row)
		if scanErr != nil {
			return fmt.Errorf("insert budget: %w", scanErr)
		}

		if status == "active" {
			if err := deactivateOtherActiveBudgetsTx(ctx, tx, firmID, b.ID, periodStart, periodEnd); err != nil {
				return err
			}
		}

		return auditlog.Write(ctx, tx, firmID, userID, b.ID, budgetAuditEntityType, createAuditAction, map[string]any{"name": name})
	})
	if err != nil {
		return Budget{}, err
	}
	return b, nil
}

// deactivateOtherActiveBudgetsTx flips every OTHER budgets row for
// firmID with status='active' whose period overlaps [periodStart,
// periodEnd] back to 'approved' (its natural pre-active, still-valid
// state) - same "flip the others in the same transaction as the new
// row's own insert/update" shape internal/manufacturing's
// deactivateOtherActiveBOMsTx uses, called from both CreateBudget and
// UpdateBudget exactly like that function is called from both
// CreateBOM/UpdateBOM.
//
// ciaudit:ignore-firmid-check: internal helper, only called by
// CreateBudget/UpdateBudget, both of which have already run their own
// permission.IsOwner check before calling this.
func deactivateOtherActiveBudgetsTx(ctx context.Context, tx pgx.Tx, firmID, excludeBudgetID uuid.UUID, periodStart, periodEnd time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE budgets SET status = 'approved'
		WHERE firm_id = $1 AND id != $2 AND status = 'active'
			AND period_start <= $4 AND period_end >= $3
	`, firmID, excludeBudgetID, periodStart, periodEnd); err != nil {
		return fmt.Errorf("deactivate other active budgets: %w", err)
	}
	return nil
}

// UpdateBudgetInput is UpdateBudget's partial-update input.
type UpdateBudgetInput struct {
	Name     *string
	Status   *string
	Notes    *string
	Currency *string
}

// UpdateBudget applies input's changes to budgetID. Owner-gated. Same
// active-conflict handling as CreateBudget when Status transitions to
// "active".
func UpdateBudget(ctx context.Context, pool *pgxpool.Pool, firmID, userID, budgetID uuid.UUID, input UpdateBudgetInput) (Budget, error) {
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		return Budget{}, fmt.Errorf("%w: name must not be empty", ErrInvalidBudget)
	}
	if input.Status != nil && !budgetStatuses[*input.Status] {
		return Budget{}, fmt.Errorf("%w: status %q is not one of draft/approved/active/closed", ErrInvalidBudget, *input.Status)
	}

	var b Budget
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
			UPDATE budgets SET
				name = COALESCE($3, name),
				status = COALESCE($4, status),
				notes = COALESCE($5, notes),
				currency = COALESCE($6, currency)
			WHERE id = $1 AND firm_id = $2
			RETURNING `+budgetColumns, budgetID, firmID, input.Name, input.Status, input.Notes, input.Currency)
		var scanErr error
		b, scanErr = scanBudgetRow(row)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ErrBudgetNotFound
		}
		if scanErr != nil {
			return fmt.Errorf("update budget: %w", scanErr)
		}

		if input.Status != nil && *input.Status == "active" {
			if err := deactivateOtherActiveBudgetsTx(ctx, tx, firmID, b.ID, b.PeriodStart, b.PeriodEnd); err != nil {
				return err
			}
		}

		return auditlog.Write(ctx, tx, firmID, userID, b.ID, budgetAuditEntityType, updateAuditAction, map[string]any{})
	})
	if err != nil {
		return Budget{}, err
	}
	return b, nil
}

// ApproveBudget transitions budgetID from 'draft' to 'approved'.
// Owner-gated. Does NOT touch the active-conflict check - approving a
// budget is not the same as activating it (a firm may approve several
// budgets, e.g. next year's alongside this year's, before deciding which
// one to actually activate).
func ApproveBudget(ctx context.Context, pool *pgxpool.Pool, firmID, userID, budgetID uuid.UUID) (Budget, error) {
	var b Budget
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

		row := tx.QueryRow(ctx, `SELECT `+budgetColumns+` FROM budgets WHERE id = $1 AND firm_id = $2 FOR UPDATE`, budgetID, firmID)
		var scanErr error
		b, scanErr = scanBudgetRow(row)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ErrBudgetNotFound
		}
		if scanErr != nil {
			return fmt.Errorf("look up budget: %w", scanErr)
		}
		if b.Status != "draft" {
			return fmt.Errorf("%w: cannot approve a budget in status %q", ErrInvalidBudgetTransition, b.Status)
		}

		row = tx.QueryRow(ctx, `
			UPDATE budgets SET status = 'approved', approved_by = $1, approved_at = now()
			WHERE id = $2 AND firm_id = $3
			RETURNING `+budgetColumns, userID, budgetID, firmID)
		b, scanErr = scanBudgetRow(row)
		if scanErr != nil {
			return fmt.Errorf("approve budget: %w", scanErr)
		}

		return auditlog.Write(ctx, tx, firmID, userID, b.ID, budgetAuditEntityType, approveAuditAction, map[string]any{})
	})
	if err != nil {
		return Budget{}, err
	}
	return b, nil
}

// ListBudgets returns firmID's budgets, ordered by period_start
// descending. Member-gated.
func ListBudgets(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Budget, error) {
	var budgets []Budget
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+budgetColumns+` FROM budgets WHERE firm_id = $1 ORDER BY period_start DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list budgets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBudgetRow(rows)
			if err != nil {
				return err
			}
			budgets = append(budgets, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return budgets, nil
}

// GetBudget resolves one budget by id within firmID. Member-gated.
func GetBudget(ctx context.Context, pool *pgxpool.Pool, firmID, userID, budgetID uuid.UUID) (Budget, error) {
	var b Budget
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+budgetColumns+` FROM budgets WHERE id = $1 AND firm_id = $2`, budgetID, firmID)
		var scanErr error
		b, scanErr = scanBudgetRow(row)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ErrBudgetNotFound
		}
		return scanErr
	})
	if err != nil {
		return Budget{}, err
	}
	return b, nil
}

const budgetColumns = `id, firm_id, name, period_type, period_start, period_end, status, currency, notes, approved_by, approved_at, created_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanBudgetRow(row rowScanner) (Budget, error) {
	var b Budget
	if err := row.Scan(&b.ID, &b.FirmID, &b.Name, &b.PeriodType, &b.PeriodStart, &b.PeriodEnd, &b.Status, &b.Currency, &b.Notes, &b.ApprovedBy, &b.ApprovedAt, &b.CreatedAt); err != nil {
		return Budget{}, err
	}
	return b, nil
}
