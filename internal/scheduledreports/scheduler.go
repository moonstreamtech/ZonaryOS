// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package scheduledreports

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
	"github.com/moonstreamtech/ZonaryOS/internal/reports"
)

// DefaultSchedulerPollInterval mirrors the other background schedulers
// in this codebase (internal/asset.MaintenanceSchedulerPollInterval,
// internal/integration.DefaultInboundPollInterval) - frequent enough
// that a "daily" scheduled report is never more than this long overdue,
// infrequent enough this isn't a busy-poll loop. A due-report check
// itself is cheap (one indexed query per firm, scheduled_reports_next_run_at_idx),
// so polling this often costs little even when nothing is due.
const DefaultSchedulerPollInterval = 15 * time.Minute

// RunScheduler runs ProcessDueScheduledReports every pollInterval until
// ctx is cancelled - the same RunX/ProcessX split every other background
// scheduler in this codebase already uses.
func RunScheduler(ctx context.Context, pool *pgxpool.Pool, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ProcessDueScheduledReports(ctx, pool)
		}
	}
}

// ProcessDueScheduledReports is RunScheduler's testable core: for every
// firm, every active scheduled_reports row whose next_run_at has passed
// is run (internal/reports.RunReport, the exact same engine the report
// definition's own manual "run" button uses) and its recipients
// notified (internal/notification.CreateForRecipientsTx) with a result
// summary - row count plus, when the report's own query_spec has no
// group_by, its single row's own metric values (the "top-line numbers"
// this batch's own brief asks for; a grouped report's per-bucket detail
// belongs in the report itself, not squeezed into a notification body).
func ProcessDueScheduledReports(ctx context.Context, pool *pgxpool.Pool) {
	firmIDs, err := listAllFirmIDs(ctx, pool)
	if err != nil {
		slog.Error("scheduled reports: list firms", "err", err)
		return
	}
	for _, firmID := range firmIDs {
		processFirmScheduledReports(ctx, pool, firmID)
	}
}

// listAllFirmIDs mirrors every other scheduler's own function of the
// same name - firms carries no RLS at all, it IS the tenant boundary.
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

// ciaudit:ignore-firmid-check: called only by ProcessDueScheduledReports,
// with firmID drawn from listAllFirmIDs (an internal, unscoped read of
// the firms table itself) - never from an HTTP caller.
func processFirmScheduledReports(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) {
	dueIDs, err := dueScheduledReportIDsTx(ctx, pool, firmID)
	if err != nil {
		slog.Error("scheduled reports: list due reports", "firmId", firmID, "err", err)
		return
	}
	if len(dueIDs) == 0 {
		return
	}

	ownerID, err := resolveFirmOwnerUserID(ctx, pool, firmID)
	if err != nil || ownerID == uuid.Nil {
		slog.Error("scheduled reports: resolve firm owner", "firmId", firmID, "err", err)
		return
	}

	for _, id := range dueIDs {
		runOneScheduledReport(ctx, pool, firmID, ownerID, id)
	}
}

// ciaudit:ignore-firmid-check: internal helper called only by
// processFirmScheduledReports - see that function's own comment for why
// firmID here is never caller-supplied.
func dueScheduledReportIDsTx(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id FROM scheduled_reports
			WHERE firm_id = $1 AND is_active AND next_run_at IS NOT NULL AND next_run_at <= now()
		`, firmID)
		if err != nil {
			return fmt.Errorf("list due scheduled reports: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// ciaudit:ignore-firmid-check: internal helper called only by
// processFirmScheduledReports - see that function's own comment for why
// firmID here is never caller-supplied. RunReport (called below) runs
// its own IsMember check against ownerID.
func runOneScheduledReport(ctx context.Context, pool *pgxpool.Pool, firmID, ownerID, scheduledReportID uuid.UUID) {
	var scheduled ScheduledReport
	if err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+scheduledReportColumns+` FROM scheduled_reports WHERE id = $1 AND firm_id = $2`, scheduledReportID, firmID)
		s, err := scanScheduledReport(row)
		if err != nil {
			return err
		}
		scheduled = s
		return nil
	}); err != nil {
		slog.Error("scheduled reports: read scheduled report", "firmId", firmID, "scheduledReportId", scheduledReportID, "err", err)
		return
	}

	rows, runErr := reports.RunReport(ctx, pool, firmID, ownerID, scheduled.DefinitionID)

	now := time.Now().UTC()
	nextRun := nextRunAfter(now, scheduled.ScheduleInterval)
	if err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE scheduled_reports SET last_run_at = $1, next_run_at = $2 WHERE id = $3 AND firm_id = $4
		`, now, nextRun, scheduledReportID, firmID); err != nil {
			return fmt.Errorf("update scheduled report run times: %w", err)
		}

		title := fmt.Sprintf("Scheduled report: %s", scheduled.Name)
		body := summarizeReportRun(rows, runErr)
		payload := map[string]any{"scheduledReportId": scheduledReportID.String(), "definitionId": scheduled.DefinitionID.String()}
		return notification.CreateForRecipientsTx(ctx, tx, firmID, scheduled.RecipientUserIDs, "scheduled_report", title, body, payload)
	}); err != nil {
		slog.Error("scheduled reports: record run / notify recipients", "firmId", firmID, "scheduledReportId", scheduledReportID, "err", err)
	}
}

// summarizeReportRun builds the notification body's own "row count,
// top-line numbers" summary - see ProcessDueScheduledReports' own doc
// comment for why only an un-grouped (single-row) report's metrics are
// included inline.
func summarizeReportRun(rows []reports.ReportRow, runErr error) string {
	if runErr != nil {
		return fmt.Sprintf("Report run failed: %v", runErr)
	}
	if len(rows) == 1 && rows[0].GroupKey == nil {
		return fmt.Sprintf("1 row. %v", rows[0].Metrics)
	}
	return fmt.Sprintf("%d rows", len(rows))
}

// resolveFirmOwnerUserID returns one of firmID's own owner users -
// scheduled report runs are background, system-triggered operations
// with no real human caller, but RunReport is member-gated; attributing
// the run to a real owner user is the same "the schedule's own creation
// was itself an owner-authorized decision, acting on its behalf is
// consistent" reasoning internal/integration/inbound.go's own identical
// helper establishes for its own no-human-caller case.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// processFirmScheduledReports - see that function's own comment for why
// firmID here is never caller-supplied.
func resolveFirmOwnerUserID(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) (uuid.UUID, error) {
	var ownerID uuid.UUID
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT ufr.user_id
			FROM user_firm_roles ufr
			JOIN roles r ON r.id = ufr.role_id AND r.firm_id = ufr.firm_id
			WHERE ufr.firm_id = $1 AND r.is_owner
			ORDER BY ufr.user_id
			LIMIT 1
		`, firmID).Scan(&ownerID)
		if err == pgx.ErrNoRows {
			ownerID = uuid.Nil
			return nil
		}
		return err
	})
	if err != nil {
		return uuid.UUID{}, err
	}
	return ownerID, nil
}
