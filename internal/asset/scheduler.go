// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Maintenance-due notification scheduler: a new, dedicated background
// goroutine (not an extension of internal/workflow's own
// scheduled_rule_runs scheduler) that checks daily for maintenance
// schedules coming due and notifies the assigned person. It is NOT a
// reuse of internal/workflow.RunScheduler - that goroutine only ever
// fires on scheduled_rule_runs rows created by defining a schedule-
// triggered workflow rule, a mechanism this batch has no reason to force
// maintenance schedules through (a maintenance_schedules row is not a
// workflow_rules row, and giving it one would mean synthesizing a fake
// rule per schedule for no benefit). What IS reused is the *shape*: same
// ticker-driven RunX(ctx, pool, pollInterval) + testable ProcessX(ctx,
// pool) split, same listAllFirmIDs-then-per-firm-WithFirmContext
// iteration, same "no distributed locking, single-process, single-
// instance deployment" scope boundary as internal/workflow/scheduler.go's
// own doc comment explains.
//
// Recipient resolution: maintenance_schedules.assigned_to_person_id
// references people(id), not users(id) - internal/hr's people rows have
// no linked user account at all (a "person" can be a contractor never
// invited to log in). internal/notification.CreateForRecipientsTx needs
// real users.id values, so this scheduler notifies every user holding an
// owner role in the firm instead (the same recipient set
// internal/permission.IsOwner itself checks against) - the schedule's
// assigned person still appears by name in the notification body, but
// the notification recipient is whichever real user account(s) can
// actually act on it.
package asset

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/notification"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// MaintenanceSchedulerPollInterval is the background scheduler's
// production poll cadence - "checks daily" per this batch's own brief.
// Tests pass a much shorter interval directly to RunMaintenanceScheduler
// so a real end-to-end firing can be observed without waiting a full day.
const MaintenanceSchedulerPollInterval = 24 * time.Hour

// maintenanceDueLookaheadDays is how far ahead of next_due_at a schedule
// counts as "coming due" - "next_due_at <= now() + 7 days" per this
// batch's own brief.
const maintenanceDueLookaheadDays = 7

const notificationTypeMaintenanceDue = "maintenance_due"

// RunMaintenanceScheduler runs ProcessDueMaintenanceSchedules every
// pollInterval until ctx is cancelled - cmd/server/main.go starts this
// with MaintenanceSchedulerPollInterval; tests pass a short interval
// (e.g. 100ms) to observe a real firing without waiting on real-world
// days.
func RunMaintenanceScheduler(ctx context.Context, pool *pgxpool.Pool, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ProcessDueMaintenanceSchedules(ctx, pool)
		}
	}
}

// ProcessDueMaintenanceSchedules is RunMaintenanceScheduler's testable
// core: find every active maintenance schedule whose next_due_at is
// within maintenanceDueLookaheadDays, across every firm, and notify that
// firm's owners - at most once per schedule per calendar day (see
// alreadyNotifiedTodayTx below), so a poll interval shorter than a day
// (as tests use) doesn't spam duplicate notifications.
func ProcessDueMaintenanceSchedules(ctx context.Context, pool *pgxpool.Pool) {
	firmIDs, err := listAllFirmIDs(ctx, pool)
	if err != nil {
		slog.Error("asset scheduler: list firms", "err", err)
		return
	}
	for _, firmID := range firmIDs {
		processDueSchedulesForFirm(ctx, pool, firmID)
	}
}

// listAllFirmIDs reads every firms.id - firms carries no RLS at all (it
// IS the tenant boundary, not tenant-scoped data), the same justification
// internal/workflow/scheduler.go's own listAllFirmIDs gives for the
// identical query.
func listAllFirmIDs(ctx context.Context, pool *pgxpool.Pool) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `SELECT id FROM firms`)
	if err != nil {
		return nil, fmt.Errorf("list firms: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan firm id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type dueMaintenanceSchedule struct {
	id                 uuid.UUID
	assetID            uuid.UUID
	name               string
	assetName          string
	nextDueAt          time.Time
	assignedToPersonID *uuid.UUID
	assignedToName     *string
}

// ciaudit:ignore-firmid-check: called only by ProcessDueMaintenanceSchedules,
// with firmID drawn from listAllFirmIDs (an internal, unscoped read of
// the firms table itself) - never from an HTTP caller.
func processDueSchedulesForFirm(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) {
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT ms.id, ms.asset_id, ms.name, a.name, ms.next_due_at, ms.assigned_to_person_id, p.full_name
			FROM maintenance_schedules ms
			JOIN assets a ON a.id = ms.asset_id
			LEFT JOIN people p ON p.id = ms.assigned_to_person_id
			WHERE ms.firm_id = $1 AND ms.is_active
				AND ms.next_due_at IS NOT NULL
				AND ms.next_due_at <= (now() + ($2 * INTERVAL '1 day'))
		`, firmID, maintenanceDueLookaheadDays)
		if err != nil {
			return fmt.Errorf("list due maintenance schedules: %w", err)
		}
		var due []dueMaintenanceSchedule
		for rows.Next() {
			var d dueMaintenanceSchedule
			if err := rows.Scan(&d.id, &d.assetID, &d.name, &d.assetName, &d.nextDueAt, &d.assignedToPersonID, &d.assignedToName); err != nil {
				rows.Close()
				return fmt.Errorf("scan due maintenance schedule: %w", err)
			}
			due = append(due, d)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()

		if len(due) == 0 {
			return nil
		}

		recipients, err := listOwnerUserIDsTx(ctx, tx, firmID)
		if err != nil {
			return err
		}
		if len(recipients) == 0 {
			return nil
		}

		for _, d := range due {
			alreadyNotified, err := alreadyNotifiedTodayTx(ctx, tx, firmID, d.id)
			if err != nil {
				return err
			}
			if alreadyNotified {
				continue
			}

			assignee := "unassigned"
			if d.assignedToName != nil {
				assignee = *d.assignedToName
			}
			title := fmt.Sprintf("Maintenance due: %s", d.assetName)
			body := fmt.Sprintf("%q on asset %q is due %s (assigned to %s).", d.name, d.assetName, d.nextDueAt.Format("2006-01-02"), assignee)
			payload := map[string]any{
				"scheduleId": d.id.String(),
				"assetId":    d.assetID.String(),
				"nextDueAt":  d.nextDueAt.Format("2006-01-02"),
			}
			if err := notification.CreateForRecipientsTx(ctx, tx, firmID, recipients, notificationTypeMaintenanceDue, title, body, payload); err != nil {
				return fmt.Errorf("notify maintenance due for schedule %s: %w", d.id, err)
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("asset scheduler: process due schedules", "firmId", firmID, "err", err)
	}
}

// alreadyNotifiedTodayTx reports whether a maintenance_due notification
// for scheduleID has already been created today - keeps a poll interval
// shorter than a day (as tests use) from creating duplicate notifications
// for the same schedule on the same calendar day.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// processDueSchedulesForFirm, itself only reachable from the background
// scheduler with a firmID drawn from listAllFirmIDs (an internal,
// unscoped read of the firms table itself) - never from an HTTP caller,
// so there is no caller-supplied firmID to validate membership against
// here.
func alreadyNotifiedTodayTx(ctx context.Context, tx pgx.Tx, firmID, scheduleID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM notifications
			WHERE firm_id = $1 AND type = $2 AND payload->>'scheduleId' = $3
				AND created_at::date = now()::date
		)
	`, firmID, notificationTypeMaintenanceDue, scheduleID.String()).Scan(&exists); err != nil {
		return false, fmt.Errorf("check existing maintenance due notification: %w", err)
	}
	return exists, nil
}

// listOwnerUserIDsTx returns every user holding an owner role in firmID -
// the same recipient set internal/permission.IsOwner itself checks
// against, and this scheduler's own doc comment explains why (assigned
// people have no guaranteed linked user account).
//
// ciaudit:ignore-firmid-check: internal helper called only by
// processDueSchedulesForFirm - see alreadyNotifiedTodayTx's own comment
// above for why firmID here is never caller-supplied.
func listOwnerUserIDsTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT ufr.user_id
		FROM user_firm_roles ufr
		JOIN roles r ON r.id = ufr.role_id AND r.firm_id = ufr.firm_id
		WHERE ufr.firm_id = $1 AND r.is_owner
	`, firmID)
	if err != nil {
		return nil, fmt.Errorf("list owner user ids: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan owner user id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
