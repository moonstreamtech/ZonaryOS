// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package scheduledreports is Part 4 of the analytics/BI/advanced
// reporting batch: combines internal/reports' own report engine
// (RunReport) with internal/notification's own inbox (CreateForRecipientsTx)
// on a schedule. It is a THIRD package, importing both, rather than
// folding into either one - internal/reports already has its own,
// unrelated public surface (query specs, KPIs, cohort analysis) and
// shouldn't need to know about notifications; internal/notification is
// deliberately generic (see its own doc comment) and shouldn't need to
// know what a "report" is. A new, small package that imports both is the
// same "build the integration point in its own package rather than
// coupling the two existing ones" move internal/workflow's plugin-hook
// bridging and internal/integration's own outbound-dispatch func-var
// hooks already use elsewhere in this codebase for an analogous
// cross-package problem - here there's no import-cycle risk (neither
// internal/reports nor internal/notification imports the other), so the
// simpler "just import both directly" version suffices, no func-var
// indirection needed.
package scheduledreports

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// ScheduleInterval is scheduled_reports.schedule_interval's closed
// vocabulary - a real DB CHECK constraint (migrations/0040).
type ScheduleInterval string

const (
	IntervalDaily   ScheduleInterval = "daily"
	IntervalWeekly  ScheduleInterval = "weekly"
	IntervalMonthly ScheduleInterval = "monthly"
)

func validInterval(i ScheduleInterval) bool {
	switch i {
	case IntervalDaily, IntervalWeekly, IntervalMonthly:
		return true
	default:
		return false
	}
}

// nextRunAfter computes the next run time after from, per interval - the
// same computation used both when a scheduled report is created/updated
// (its first next_run_at) and after each scheduler run (its next one).
func nextRunAfter(from time.Time, interval ScheduleInterval) time.Time {
	switch interval {
	case IntervalDaily:
		return from.AddDate(0, 0, 1)
	case IntervalWeekly:
		return from.AddDate(0, 0, 7)
	case IntervalMonthly:
		return from.AddDate(0, 1, 0)
	default:
		return from.AddDate(0, 0, 1)
	}
}

// ScheduledReport is one scheduled_reports row.
type ScheduledReport struct {
	ID               uuid.UUID
	FirmID           uuid.UUID
	DefinitionID     uuid.UUID
	Name             string
	ScheduleInterval ScheduleInterval
	LastRunAt        *time.Time
	NextRunAt        *time.Time
	RecipientUserIDs []uuid.UUID
	IsActive         bool
	CreatedAt        time.Time
}

// ScheduledReportInput is CreateScheduledReport/UpdateScheduledReport's
// shared field shape.
type ScheduledReportInput struct {
	DefinitionID     uuid.UUID
	Name             string
	ScheduleInterval ScheduleInterval
	RecipientUserIDs []uuid.UUID
	IsActive         bool
}

func validateInput(input ScheduledReportInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("%w: name must not be empty", ErrInvalidScheduledReport)
	}
	if !validInterval(input.ScheduleInterval) {
		return fmt.Errorf("%w: unknown scheduleInterval %q", ErrInvalidScheduledReport, input.ScheduleInterval)
	}
	if input.DefinitionID == uuid.Nil {
		return fmt.Errorf("%w: definitionId must not be empty", ErrInvalidScheduledReport)
	}
	return nil
}

const scheduledReportColumns = "id, firm_id, definition_id, name, schedule_interval, last_run_at, next_run_at, recipient_user_ids, is_active, created_at"

func scanScheduledReport(row pgx.Row) (ScheduledReport, error) {
	var s ScheduledReport
	var intervalRaw string
	if err := row.Scan(&s.ID, &s.FirmID, &s.DefinitionID, &s.Name, &intervalRaw, &s.LastRunAt, &s.NextRunAt,
		&s.RecipientUserIDs, &s.IsActive, &s.CreatedAt); err != nil {
		return ScheduledReport{}, err
	}
	s.ScheduleInterval = ScheduleInterval(intervalRaw)
	return s, nil
}

// CreateScheduledReport adds a new scheduled_reports row - owner-gated:
// deciding who automatically receives a recurring report is a
// structural firm decision, the same tier as internal/webhook.CreateWebhook.
// definitionID is validated to belong to firmID (a report_definitions
// row visible via the firm's own RLS scope) - not owned by any specific
// user, just required to exist.
func CreateScheduledReport(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input ScheduledReportInput) (ScheduledReport, error) {
	if err := validateInput(input); err != nil {
		return ScheduledReport{}, err
	}

	var scheduled ScheduledReport
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

		var definitionExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM report_definitions WHERE id = $1 AND firm_id = $2)`, input.DefinitionID, firmID).Scan(&definitionExists); err != nil {
			return fmt.Errorf("check report definition exists: %w", err)
		}
		if !definitionExists {
			return fmt.Errorf("%w: report definition not found", ErrInvalidScheduledReport)
		}

		nextRun := nextRunAfter(time.Now().UTC(), input.ScheduleInterval)
		row := tx.QueryRow(ctx, `
			INSERT INTO scheduled_reports (firm_id, definition_id, name, schedule_interval, next_run_at, recipient_user_ids, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING `+scheduledReportColumns,
			firmID, input.DefinitionID, strings.TrimSpace(input.Name), string(input.ScheduleInterval), nextRun, input.RecipientUserIDs, input.IsActive,
		)
		s, err := scanScheduledReport(row)
		if err != nil {
			return fmt.Errorf("insert scheduled report: %w", err)
		}
		scheduled = s
		return nil
	})
	if err != nil {
		return ScheduledReport{}, err
	}
	return scheduled, nil
}

// ListScheduledReports returns every scheduled report for firmID -
// member-gated read, same tier as internal/webhook.ListWebhooks.
func ListScheduledReports(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]ScheduledReport, error) {
	var reports []ScheduledReport
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+scheduledReportColumns+` FROM scheduled_reports WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list scheduled reports: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			s, err := scanScheduledReport(rows)
			if err != nil {
				return fmt.Errorf("scan scheduled report: %w", err)
			}
			reports = append(reports, s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return reports, nil
}

// UpdateScheduledReport replaces scheduledReportID's mutable fields -
// owner-gated, full-replace, same tier as CreateScheduledReport.
// Recomputes next_run_at from now() if scheduleInterval changed to keep
// the schedule consistent with its own new cadence, rather than leaving
// a stale next_run_at computed under the old interval.
func UpdateScheduledReport(ctx context.Context, pool *pgxpool.Pool, firmID, userID, scheduledReportID uuid.UUID, input ScheduledReportInput) (ScheduledReport, error) {
	if err := validateInput(input); err != nil {
		return ScheduledReport{}, err
	}

	var scheduled ScheduledReport
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

		nextRun := nextRunAfter(time.Now().UTC(), input.ScheduleInterval)
		row := tx.QueryRow(ctx, `
			UPDATE scheduled_reports SET name = $1, schedule_interval = $2, next_run_at = $3,
				recipient_user_ids = $4, is_active = $5
			WHERE id = $6 AND firm_id = $7
			RETURNING `+scheduledReportColumns,
			strings.TrimSpace(input.Name), string(input.ScheduleInterval), nextRun, input.RecipientUserIDs, input.IsActive, scheduledReportID, firmID,
		)
		s, err := scanScheduledReport(row)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrScheduledReportNotFound
			}
			return fmt.Errorf("update scheduled report: %w", err)
		}
		scheduled = s
		return nil
	})
	if err != nil {
		return ScheduledReport{}, err
	}
	return scheduled, nil
}

// DeleteScheduledReport removes scheduledReportID - owner-gated, same
// tier as CreateScheduledReport.
func DeleteScheduledReport(ctx context.Context, pool *pgxpool.Pool, firmID, userID, scheduledReportID uuid.UUID) error {
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

		tag, err := tx.Exec(ctx, `DELETE FROM scheduled_reports WHERE id = $1 AND firm_id = $2`, scheduledReportID, firmID)
		if err != nil {
			return fmt.Errorf("delete scheduled report: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrScheduledReportNotFound
		}
		return nil
	})
}
