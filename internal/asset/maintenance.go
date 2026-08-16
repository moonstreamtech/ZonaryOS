// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package asset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const (
	maintenanceScheduleAuditEntityType = "maintenance_schedule"
	maintenanceRecordAuditEntityType   = "maintenance_record"
	maintenanceRecordSourceType        = "maintenance_record"
)

// MaintenanceSchedule is one maintenance_schedules row. NextDueAt is
// GENERATED ALWAYS in Postgres (last_done_at + interval_days days) - see
// migrations/0030_asset_management.up.sql's own doc comment for why a
// STORED generated column was chosen over computing this at read time.
type MaintenanceSchedule struct {
	ID                 uuid.UUID
	FirmID             uuid.UUID
	AssetID            uuid.UUID
	Name               string
	IntervalDays       int
	LastDoneAt         *time.Time
	NextDueAt          *time.Time
	AssignedToPersonID *uuid.UUID
	IsActive           bool
	CreatedAt          time.Time
}

// CreateMaintenanceScheduleInput is CreateMaintenanceSchedule's request
// shape.
type CreateMaintenanceScheduleInput struct {
	Name               string
	IntervalDays       int
	LastDoneAt         string // "YYYY-MM-DD", optional (parsed via parseOptionalDate)
	AssignedToPersonID *uuid.UUID
}

// CreateMaintenanceSchedule adds a new maintenance schedule to assetID
// within firmID. Owner-gated.
func CreateMaintenanceSchedule(ctx context.Context, pool *pgxpool.Pool, firmID, userID, assetID uuid.UUID, input CreateMaintenanceScheduleInput) (MaintenanceSchedule, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return MaintenanceSchedule{}, fmt.Errorf("%w: name must not be empty", ErrInvalidMaintenanceSchedule)
	}
	if input.IntervalDays <= 0 {
		return MaintenanceSchedule{}, fmt.Errorf("%w: interval_days must be positive", ErrInvalidMaintenanceSchedule)
	}
	lastDoneAt, err := parseOptionalDate(input.LastDoneAt)
	if err != nil {
		return MaintenanceSchedule{}, fmt.Errorf("%w: lastDoneAt must be YYYY-MM-DD", ErrInvalidMaintenanceSchedule)
	}

	var s MaintenanceSchedule
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

		exists, err := AssetExistsTx(ctx, tx, firmID, assetID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrAssetNotFound
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO maintenance_schedules (firm_id, asset_id, name, interval_days, last_done_at, assigned_to_person_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, firm_id, asset_id, name, interval_days, last_done_at, next_due_at, assigned_to_person_id, is_active, created_at
		`, firmID, assetID, name, input.IntervalDays, lastDoneAt, input.AssignedToPersonID)
		var scanErr error
		s, scanErr = scanScheduleRow(row)
		if scanErr != nil {
			return fmt.Errorf("insert maintenance schedule: %w", scanErr)
		}

		return auditlog.Write(ctx, tx, firmID, userID, s.ID, maintenanceScheduleAuditEntityType, createAuditAction, map[string]any{"name": name, "assetId": assetID})
	})
	if err != nil {
		return MaintenanceSchedule{}, err
	}
	return s, nil
}

// UpdateMaintenanceScheduleInput is UpdateMaintenanceSchedule's
// partial-update input.
type UpdateMaintenanceScheduleInput struct {
	Name               *string
	IntervalDays       *int
	AssignedToPersonID *uuid.UUID
	HasAssignedTo      bool
	IsActive           *bool
}

// UpdateMaintenanceSchedule applies input's changes to scheduleID.
// Owner-gated.
func UpdateMaintenanceSchedule(ctx context.Context, pool *pgxpool.Pool, firmID, userID, scheduleID uuid.UUID, input UpdateMaintenanceScheduleInput) (MaintenanceSchedule, error) {
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		return MaintenanceSchedule{}, fmt.Errorf("%w: name must not be empty", ErrInvalidMaintenanceSchedule)
	}
	if input.IntervalDays != nil && *input.IntervalDays <= 0 {
		return MaintenanceSchedule{}, fmt.Errorf("%w: interval_days must be positive", ErrInvalidMaintenanceSchedule)
	}

	var s MaintenanceSchedule
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
			UPDATE maintenance_schedules SET
				name = COALESCE($3, name),
				interval_days = COALESCE($4, interval_days),
				assigned_to_person_id = CASE WHEN $5::boolean THEN $6 ELSE assigned_to_person_id END,
				is_active = COALESCE($7, is_active)
			WHERE id = $1 AND firm_id = $2
			RETURNING id, firm_id, asset_id, name, interval_days, last_done_at, next_due_at, assigned_to_person_id, is_active, created_at
		`, scheduleID, firmID, input.Name, input.IntervalDays, input.HasAssignedTo, input.AssignedToPersonID, input.IsActive)
		var scanErr error
		s, scanErr = scanScheduleRow(row)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return ErrMaintenanceScheduleNotFound
		}
		if scanErr != nil {
			return fmt.Errorf("update maintenance schedule: %w", scanErr)
		}

		return auditlog.Write(ctx, tx, firmID, userID, s.ID, maintenanceScheduleAuditEntityType, updateAuditAction, map[string]any{})
	})
	if err != nil {
		return MaintenanceSchedule{}, err
	}
	return s, nil
}

// ListMaintenanceSchedules returns assetID's maintenance schedules within
// firmID, ordered by name. Member-gated.
func ListMaintenanceSchedules(ctx context.Context, pool *pgxpool.Pool, firmID, userID, assetID uuid.UUID) ([]MaintenanceSchedule, error) {
	var schedules []MaintenanceSchedule
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, asset_id, name, interval_days, last_done_at, next_due_at, assigned_to_person_id, is_active, created_at
			FROM maintenance_schedules WHERE firm_id = $1 AND asset_id = $2 ORDER BY name
		`, firmID, assetID)
		if err != nil {
			return fmt.Errorf("list maintenance schedules: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			s, err := scanScheduleRow(rows)
			if err != nil {
				return err
			}
			schedules = append(schedules, s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return schedules, nil
}

// MaintenanceRecord is one maintenance_records row.
type MaintenanceRecord struct {
	ID                  uuid.UUID
	FirmID              uuid.UUID
	AssetID             uuid.UUID
	ScheduleID          *uuid.UUID
	PerformedAt         time.Time
	PerformedByPersonID *uuid.UUID
	Description         string
	Cost                *string
	NextServiceDate     *time.Time
	CreatedAt           time.Time
}

// CreateMaintenanceRecordInput is CreateMaintenanceRecord's request shape.
// PerformedAt/NextServiceDate are plain "YYYY-MM-DD" strings.
type CreateMaintenanceRecordInput struct {
	ScheduleID          *uuid.UUID
	PerformedAt         string
	PerformedByPersonID *uuid.UUID
	Description         string
	Cost                *string
	NextServiceDate     string
}

// CreateMaintenanceRecord logs a completed maintenance action against
// assetID within firmID. Owner-gated.
//
// Two side effects, both inside the same transaction as the insert:
//  1. If ScheduleID is set, that schedule's last_done_at is advanced to
//     PerformedAt - which Postgres then recomputes next_due_at from
//     automatically (the GENERATED ALWAYS column), so this function never
//     computes or writes next_due_at itself.
//  2. If Cost is set (non-nil, non-empty), a real journal entry posts: DR
//     Maintenance Expense / CR Cash if the firm has a Cash account seeded,
//     else CR Trade Payables (an unpaid maintenance bill still owed) - the
//     same "pick whichever of two credit accounts actually exists"
//     judgment call this function is the first instance of in this
//     codebase (see this batch's own report for why no prior precedent
//     existed to copy verbatim).
func CreateMaintenanceRecord(ctx context.Context, pool *pgxpool.Pool, firmID, userID, assetID uuid.UUID, input CreateMaintenanceRecordInput) (MaintenanceRecord, error) {
	description := strings.TrimSpace(input.Description)
	if description == "" {
		return MaintenanceRecord{}, fmt.Errorf("%w: description must not be empty", ErrInvalidMaintenanceRecord)
	}
	performedAt, err := parseOptionalDate(input.PerformedAt)
	if err != nil || performedAt == nil {
		return MaintenanceRecord{}, fmt.Errorf("%w: performedAt must be a valid YYYY-MM-DD date", ErrInvalidMaintenanceRecord)
	}
	nextServiceDate, err := parseOptionalDate(input.NextServiceDate)
	if err != nil {
		return MaintenanceRecord{}, fmt.Errorf("%w: nextServiceDate must be YYYY-MM-DD", ErrInvalidMaintenanceRecord)
	}

	var r MaintenanceRecord
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

		exists, err := AssetExistsTx(ctx, tx, firmID, assetID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrAssetNotFound
		}

		if input.ScheduleID != nil {
			var scheduleAssetID uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT asset_id FROM maintenance_schedules WHERE id = $1 AND firm_id = $2`, *input.ScheduleID, firmID).Scan(&scheduleAssetID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrMaintenanceScheduleNotFound
				}
				return fmt.Errorf("look up maintenance schedule: %w", err)
			}
			if scheduleAssetID != assetID {
				return fmt.Errorf("%w: schedule_id does not belong to this asset", ErrInvalidMaintenanceRecord)
			}
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO maintenance_records (firm_id, asset_id, schedule_id, performed_at, performed_by_person_id, description, cost, next_service_date)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id, firm_id, asset_id, schedule_id, performed_at, performed_by_person_id, description, cost::text, next_service_date, created_at
		`, firmID, assetID, input.ScheduleID, performedAt, input.PerformedByPersonID, description, input.Cost, nextServiceDate)
		var scanErr error
		r, scanErr = scanRecordRow(row)
		if scanErr != nil {
			return fmt.Errorf("insert maintenance record: %w", scanErr)
		}

		// Side effect 1: advance the schedule's last_done_at -
		// next_due_at recomputes automatically (GENERATED ALWAYS).
		if input.ScheduleID != nil {
			if _, err := tx.Exec(ctx, `UPDATE maintenance_schedules SET last_done_at = $1 WHERE id = $2 AND firm_id = $3`, performedAt, *input.ScheduleID, firmID); err != nil {
				return fmt.Errorf("advance maintenance schedule last_done_at: %w", err)
			}
		}

		// Side effect 2: post a journal entry if a cost was recorded.
		if input.Cost != nil && strings.TrimSpace(*input.Cost) != "" && strings.TrimSpace(*input.Cost) != "0" {
			var hasCashAccount bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE firm_id = $1 AND code = $2)`, firmID, accounting.CashAccountCode).Scan(&hasCashAccount); err != nil {
				return fmt.Errorf("check cash account: %w", err)
			}
			creditCode := accounting.TradePayablesAccountCode
			if hasCashAccount {
				creditCode = accounting.CashAccountCode
			}
			lines := []accounting.LineInput{
				{AccountCode: accounting.MaintenanceExpenseAccountCode, Side: accounting.SideDebit, Amount: *input.Cost},
				{AccountCode: creditCode, Side: accounting.SideCredit, Amount: *input.Cost},
			}
			if _, err := accounting.PostJournalEntryTx(ctx, tx, firmID, userID, "Maintenance: "+description, maintenanceRecordSourceType, &r.ID, lines); err != nil {
				return err
			}
		}

		return auditlog.Write(ctx, tx, firmID, userID, r.ID, maintenanceRecordAuditEntityType, createAuditAction, map[string]any{"assetId": assetID})
	})
	if err != nil {
		return MaintenanceRecord{}, err
	}
	return r, nil
}

// ListMaintenanceRecords returns assetID's maintenance records within
// firmID, newest first. Member-gated.
func ListMaintenanceRecords(ctx context.Context, pool *pgxpool.Pool, firmID, userID, assetID uuid.UUID) ([]MaintenanceRecord, error) {
	var records []MaintenanceRecord
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, asset_id, schedule_id, performed_at, performed_by_person_id, description, cost::text, next_service_date, created_at
			FROM maintenance_records WHERE firm_id = $1 AND asset_id = $2 ORDER BY performed_at DESC, created_at DESC
		`, firmID, assetID)
		if err != nil {
			return fmt.Errorf("list maintenance records: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanRecordRow(rows)
			if err != nil {
				return err
			}
			records = append(records, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func scanScheduleRow(row rowScanner) (MaintenanceSchedule, error) {
	var s MaintenanceSchedule
	if err := row.Scan(&s.ID, &s.FirmID, &s.AssetID, &s.Name, &s.IntervalDays, &s.LastDoneAt, &s.NextDueAt, &s.AssignedToPersonID, &s.IsActive, &s.CreatedAt); err != nil {
		return MaintenanceSchedule{}, err
	}
	return s, nil
}

func scanRecordRow(row rowScanner) (MaintenanceRecord, error) {
	var r MaintenanceRecord
	if err := row.Scan(&r.ID, &r.FirmID, &r.AssetID, &r.ScheduleID, &r.PerformedAt, &r.PerformedByPersonID, &r.Description, &r.Cost, &r.NextServiceDate, &r.CreatedAt); err != nil {
		return MaintenanceRecord{}, err
	}
	return r, nil
}
