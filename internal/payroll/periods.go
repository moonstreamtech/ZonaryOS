// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package payroll is the payroll foundation batch's core: payroll_periods
// (a run window like "August 2026") and payroll_entries (one person's line
// within that window), plus the period-close action that posts the
// period's payroll to the general ledger. Mirrors internal/hr's own
// authorization discipline (docs/DEVELOPMENT.md's checklist): IsMember
// before any read, IsOwner before any write - payroll is firm-structural
// financial data, the same tier as internal/accounting's own writes.
//
// Scope boundary (mirrors migrations/0025's own doc comment): no tax/
// deduction *calculation* (Open Points item 24 territory - deductions is a
// stored freeform record here, never computed), no payslip document
// generation, no overtime rules.
package payroll

import (
	"context"
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

// dateLayout mirrors internal/hr's own convention exactly - a plain
// calendar-date format matching an HTML <input type="date"> value.
const dateLayout = "2006-01-02"

// amountPattern mirrors internal/accounting/internal/hr's own
// amountPattern - a plain positive decimal string, matching this
// package's own numeric(19,4) columns.
var amountPattern = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)

const periodAuditEntityType = "payroll_period"
const (
	createPeriodAuditAction = "create"
	updatePeriodAuditAction = "update"
	closePeriodAuditAction  = "close"
)

// PeriodStatus mirrors migrations/0025's payroll_periods.status CHECK
// constraint.
type PeriodStatus string

const (
	PeriodStatusOpen       PeriodStatus = "open"
	PeriodStatusProcessing PeriodStatus = "processing"
	PeriodStatusClosed     PeriodStatus = "closed"
)

// Period is one payroll_periods row.
type Period struct {
	ID          uuid.UUID
	FirmID      uuid.UUID
	Name        string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Status      PeriodStatus
	CreatedAt   time.Time
}

// CreatePeriodInput is CreatePeriod's request shape.
type CreatePeriodInput struct {
	Name        string
	PeriodStart string // "YYYY-MM-DD", required
	PeriodEnd   string // "YYYY-MM-DD", required
}

func parseRequiredDate(field, s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("%w: %s must not be empty", ErrInvalidPeriod, field)
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %s %q must be in YYYY-MM-DD format", ErrInvalidPeriod, field, s)
	}
	return t, nil
}

// CreatePeriod adds a new payroll period to firmID. Owner-gated: opening a
// payroll run is a structural financial decision, the same tier as
// internal/accounting.CreateAccount.
func CreatePeriod(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreatePeriodInput) (Period, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Period{}, fmt.Errorf("%w: name must not be empty", ErrInvalidPeriod)
	}
	start, err := parseRequiredDate("periodStart", input.PeriodStart)
	if err != nil {
		return Period{}, err
	}
	end, err := parseRequiredDate("periodEnd", input.PeriodEnd)
	if err != nil {
		return Period{}, err
	}
	if end.Before(start) {
		return Period{}, fmt.Errorf("%w: periodEnd must not be before periodStart", ErrInvalidPeriod)
	}

	var period Period
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

		period = Period{FirmID: firmID, Name: name, PeriodStart: start, PeriodEnd: end, Status: PeriodStatusOpen}
		if err := tx.QueryRow(ctx, `
			INSERT INTO payroll_periods (firm_id, name, period_start, period_end)
			VALUES ($1, $2, $3, $4)
			RETURNING id, status, created_at
		`, firmID, name, start, end).Scan(&period.ID, &period.Status, &period.CreatedAt); err != nil {
			return fmt.Errorf("insert payroll period: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, period.ID, periodAuditEntityType, createPeriodAuditAction, map[string]any{
			"name": name, "periodStart": input.PeriodStart, "periodEnd": input.PeriodEnd,
		})
	})
	if err != nil {
		return Period{}, err
	}
	return period, nil
}

// ListPeriods returns firmID's payroll periods, most recent start date
// first. Member-gated (not owner-gated): reading payroll history is
// ordinary firm data visibility, same tier as internal/hr.ListPeople -
// only creating/editing/closing a period is owner-only.
func ListPeriods(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Period, error) {
	var periods []Period
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, name, period_start, period_end, status, created_at
			FROM payroll_periods WHERE firm_id = $1 ORDER BY period_start DESC
		`, firmID)
		if err != nil {
			return fmt.Errorf("list payroll periods: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p Period
			var status string
			if err := rows.Scan(&p.ID, &p.FirmID, &p.Name, &p.PeriodStart, &p.PeriodEnd, &status, &p.CreatedAt); err != nil {
				return err
			}
			p.Status = PeriodStatus(status)
			periods = append(periods, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return periods, nil
}

// GetPeriod resolves one payroll period by id within firmID. Member-gated,
// same tier as ListPeriods.
func GetPeriod(ctx context.Context, pool *pgxpool.Pool, firmID, userID, periodID uuid.UUID) (Period, error) {
	var period Period
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		return getPeriodTx(ctx, tx, firmID, periodID, &period)
	})
	if err != nil {
		return Period{}, err
	}
	return period, nil
}

// getPeriodTx loads periodID within firmID into *out, inside the caller's
// already-authorized transaction.
//
// ciaudit:ignore-firmid-check: internal helper, only called by GetPeriod/
// UpdatePeriod/CreateEntry/ClosePeriod - each of which has already run
// IsMember/IsOwner before reaching this call - firmID here scopes the
// query, not a fresh authorization decision.
func getPeriodTx(ctx context.Context, tx pgx.Tx, firmID, periodID uuid.UUID, out *Period) error {
	var status string
	err := tx.QueryRow(ctx, `
		SELECT id, firm_id, name, period_start, period_end, status, created_at
		FROM payroll_periods WHERE id = $1 AND firm_id = $2
	`, periodID, firmID).Scan(&out.ID, &out.FirmID, &out.Name, &out.PeriodStart, &out.PeriodEnd, &status, &out.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrPeriodNotFound
		}
		return fmt.Errorf("look up payroll period: %w", err)
	}
	out.Status = PeriodStatus(status)
	return nil
}

// PeriodUpdate is UpdatePeriod's partial-update input - nil leaves that
// column unchanged, matching internal/hr.PersonUpdate's convention.
type PeriodUpdate struct {
	Name        *string
	PeriodStart *string
	PeriodEnd   *string
}

// UpdatePeriod changes periodID's mutable fields within firmID. Owner-
// gated. Rejected once the period is no longer 'open' - a processing/
// closed period's window shouldn't silently move out from under entries
// already computed against it.
func UpdatePeriod(ctx context.Context, pool *pgxpool.Pool, firmID, userID, periodID uuid.UUID, update PeriodUpdate) (Period, error) {
	var period Period
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

		if err := getPeriodTx(ctx, tx, firmID, periodID, &period); err != nil {
			return err
		}
		if period.Status != PeriodStatusOpen {
			return ErrPeriodNotOpen
		}

		name := period.Name
		if update.Name != nil {
			name = strings.TrimSpace(*update.Name)
			if name == "" {
				return fmt.Errorf("%w: name must not be empty", ErrInvalidPeriod)
			}
		}
		start := period.PeriodStart
		if update.PeriodStart != nil {
			start, err = parseRequiredDate("periodStart", *update.PeriodStart)
			if err != nil {
				return err
			}
		}
		end := period.PeriodEnd
		if update.PeriodEnd != nil {
			end, err = parseRequiredDate("periodEnd", *update.PeriodEnd)
			if err != nil {
				return err
			}
		}
		if end.Before(start) {
			return fmt.Errorf("%w: periodEnd must not be before periodStart", ErrInvalidPeriod)
		}

		var status string
		if err := tx.QueryRow(ctx, `
			UPDATE payroll_periods SET name = $1, period_start = $2, period_end = $3
			WHERE id = $4 AND firm_id = $5
			RETURNING id, firm_id, name, period_start, period_end, status, created_at
		`, name, start, end, periodID, firmID).Scan(&period.ID, &period.FirmID, &period.Name, &period.PeriodStart, &period.PeriodEnd, &status, &period.CreatedAt); err != nil {
			return fmt.Errorf("update payroll period: %w", err)
		}
		period.Status = PeriodStatus(status)

		return auditlog.Write(ctx, tx, firmID, userID, period.ID, periodAuditEntityType, updatePeriodAuditAction, map[string]any{"name": name})
	})
	if err != nil {
		return Period{}, err
	}
	return period, nil
}
