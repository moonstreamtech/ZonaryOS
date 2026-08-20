// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package alerting_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/alerting"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

func setupTest(t *testing.T) *pgxpool.Pool {
	t.Helper()
	adminDSN := os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN := os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run alerting integration tests")
	}
	ctx := context.Background()

	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	adminPool, err := zdb.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)
	if _, err := adminPool.Exec(ctx, `TRUNCATE request_metrics, alert_events, alert_rules CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err := zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(appPool.Close)
	return appPool
}

// TestProcessAlertChecks_ZeroThresholdRuleTriggersOnAnyError is this
// batch's own required test: "a rule with threshold=0 triggers on any
// error, confirmed creates alert_event + platform-admin notification."
// The notification half (a structured slog.Warn line - see
// notifyPlatformAdmins' own doc comment for why that is this batch's
// real notification channel) is covered directly by this package's own
// TestNotifyPlatformAdmins_LogsOneWarnLinePerAdminEmail unit test; this
// integration test confirms the other half against a real Postgres: one
// request with status_code=500 is enough to push error_rate above a
// threshold of 0, and ProcessAlertChecks must record exactly one
// alert_events row for it.
func TestProcessAlertChecks_ZeroThresholdRuleTriggersOnAnyError(t *testing.T) {
	pool := setupTest(t)
	ctx := context.Background()

	rule, err := alerting.CreateRule(ctx, pool, alerting.RuleInput{
		Name: "Any error at all", Metric: alerting.MetricErrorRate, Threshold: 0, WindowMinutes: 60, IsActive: true,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO request_metrics (method, path_pattern, status_code, duration_ms, occurred_at)
		VALUES ('GET', '/api/firms/{id}/products', 500, 12, now())
	`); err != nil {
		t.Fatalf("insert request metric: %v", err)
	}

	alerting.ProcessAlertChecks(ctx, pool, []string{"admin@example.com"})

	events, err := alerting.ListRecentEvents(ctx, pool, 10)
	if err != nil {
		t.Fatalf("ListRecentEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 alert event, got %d", len(events))
	}
	if events[0].RuleID != rule.ID {
		t.Errorf("expected alert event for rule %s, got %s", rule.ID, events[0].RuleID)
	}
	if events[0].MetricValue <= 0 {
		t.Errorf("expected a positive recorded error rate, got %v", events[0].MetricValue)
	}
}

// TestProcessAlertChecks_NoBreachCreatesNoEvent confirms the inverse: an
// all-2xx window never creates an alert_event for a rule whose
// threshold requires an actual breach.
func TestProcessAlertChecks_NoBreachCreatesNoEvent(t *testing.T) {
	pool := setupTest(t)
	ctx := context.Background()

	if _, err := alerting.CreateRule(ctx, pool, alerting.RuleInput{
		Name: "High error rate", Metric: alerting.MetricErrorRate, Threshold: 0.5, WindowMinutes: 60, IsActive: true,
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO request_metrics (method, path_pattern, status_code, duration_ms, occurred_at)
		VALUES ('GET', '/api/firms/{id}/products', 200, 12, now())
	`); err != nil {
		t.Fatalf("insert request metric: %v", err)
	}

	alerting.ProcessAlertChecks(ctx, pool, []string{"admin@example.com"})

	events, err := alerting.ListRecentEvents(ctx, pool, 10)
	if err != nil {
		t.Fatalf("ListRecentEvents: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected no alert events for an all-2xx window, got %d", len(events))
	}
}
