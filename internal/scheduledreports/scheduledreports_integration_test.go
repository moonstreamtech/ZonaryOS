// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package scheduledreports_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/notification"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/reports"
	"github.com/moonstreamtech/ZonaryOS/internal/scheduledreports"
)

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run scheduledreports integration tests")
	}
	return adminDSN, appDSN
}

func setupTest(t *testing.T) (adminPool, appPool *pgxpool.Pool) {
	t.Helper()
	adminDSN, appDSN := testPools(t)
	ctx := context.Background()

	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	adminPool, err := zdb.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)
	if _, err := adminPool.Exec(ctx, `
		TRUNCATE firms, users, roles, role_permissions, user_firm_roles,
			workflow_definitions, workflow_states, workflow_transitions, workflow_instances,
			report_definitions, saved_report_runs, scheduled_reports, notifications, audit_log, permissions CASCADE
	`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	appPool, err = zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(appPool.Close)
	return adminPool, appPool
}

func seedOwner(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmName, keycloakSubject string) (firmID, userID uuid.UUID) {
	t.Helper()
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ($1) RETURNING id`, firmName).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test Owner").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (firm_id, key, name, is_owner) VALUES ($1, 'owner', 'Owner', true) RETURNING id
		`, firmID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed owner role/membership: %v", err)
	}
	return firmID, userID
}

// TestProcessDueScheduledReports_RunCreatesSavedRunAndNotification is
// Part 5's own required test: a scheduled report run creates a
// saved_report_run and a notification for each recipient.
func TestProcessDueScheduledReports_RunCreatesSavedRunAndNotification(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Scheduled Report Firm", "sched-owner")

	def, err := reports.CreateReportDefinition(ctx, appPool, firmID, ownerID, reports.ReportDefinitionInput{
		Name: "Instance Count", QuerySpec: reports.QuerySpec{
			Entity:  "workflow_instances",
			Metrics: []reports.QueryMetric{{Name: "total", Aggregation: "count"}},
		},
	})
	if err != nil {
		t.Fatalf("CreateReportDefinition: %v", err)
	}

	scheduled, err := scheduledreports.CreateScheduledReport(ctx, appPool, firmID, ownerID, scheduledreports.ScheduledReportInput{
		DefinitionID: def.ID, Name: "Daily instance count", ScheduleInterval: scheduledreports.IntervalDaily,
		RecipientUserIDs: []uuid.UUID{ownerID}, IsActive: true,
	})
	if err != nil {
		t.Fatalf("CreateScheduledReport: %v", err)
	}

	// Force it due right now - CreateScheduledReport sets next_run_at
	// one interval into the future, but the scheduler under test only
	// ever picks up rows whose next_run_at has already passed.
	if _, err := adminPool.Exec(ctx, `UPDATE scheduled_reports SET next_run_at = now() - interval '1 minute' WHERE id = $1`, scheduled.ID); err != nil {
		t.Fatalf("force scheduled report due: %v", err)
	}

	scheduledreports.ProcessDueScheduledReports(ctx, appPool)

	var runCount int
	if err := adminPool.QueryRow(ctx, `SELECT COUNT(*) FROM saved_report_runs WHERE definition_id = $1`, def.ID).Scan(&runCount); err != nil {
		t.Fatalf("count saved report runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("expected exactly one saved_report_run, got %d", runCount)
	}

	result, err := notification.ListForUser(ctx, appPool, firmID, ownerID, notification.ListOptions{})
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(result.Notifications) != 1 {
		t.Fatalf("expected exactly one notification for the recipient, got %+v", result.Notifications)
	}
	if result.Notifications[0].Type != "scheduled_report" {
		t.Fatalf("expected notification type \"scheduled_report\", got %q", result.Notifications[0].Type)
	}

	updated, err := scheduledreports.ListScheduledReports(ctx, appPool, firmID, ownerID)
	if err != nil {
		t.Fatalf("ListScheduledReports: %v", err)
	}
	if len(updated) != 1 || updated[0].LastRunAt == nil {
		t.Fatalf("expected last_run_at to be set after a run, got %+v", updated)
	}
	if updated[0].NextRunAt == nil || !updated[0].NextRunAt.After(time.Now()) {
		t.Fatalf("expected next_run_at to be rescheduled into the future, got %+v", updated[0].NextRunAt)
	}
}
