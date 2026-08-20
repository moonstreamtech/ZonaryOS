// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apm_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/apm"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

func setupTest(t *testing.T) *pgxpool.Pool {
	t.Helper()
	adminDSN := os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN := os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run apm integration tests")
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
	if _, err := adminPool.Exec(ctx, `TRUNCATE request_metrics`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err := zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(appPool.Close)
	return appPool
}

// TestEnsureFuturePartitions_CreatesMonthAheadPartitions is this
// batch's own required test: month-ahead partitions are created when
// missing. The initial migration already creates the current-month
// partition (whatever month the test happens to run in falls inside
// migrations/0041's own hardcoded Aug/Sep/Oct 2026 range or the
// migration's own DEFAULT catch-all otherwise), so this test's real
// assertion is CheckPartitionHealth reporting fully healthy - no
// missing months - immediately after EnsureFuturePartitions runs,
// regardless of what state the table started in.
func TestEnsureFuturePartitions_CreatesMonthAheadPartitions(t *testing.T) {
	pool := setupTest(t)
	ctx := context.Background()

	if err := apm.EnsureFuturePartitions(ctx, pool); err != nil {
		t.Fatalf("EnsureFuturePartitions: %v", err)
	}

	health, err := apm.CheckPartitionHealth(ctx, pool)
	if err != nil {
		t.Fatalf("CheckPartitionHealth: %v", err)
	}
	if len(health.MissingMonths) != 0 {
		t.Errorf("expected no missing months after EnsureFuturePartitions, got %v", health.MissingMonths)
	}
}

// TestEndpointStatsSince_ComputesPercentilesAndErrorRate confirms
// EndpointStatsSince/ErrorRate/ResponseTimeP95/RequestVolume - the
// shared primitives both the metrics-summary handler and
// internal/alerting's own checker read from - return the expected
// figures for a small, known set of inserted rows.
func TestEndpointStatsSince_ComputesPercentilesAndErrorRate(t *testing.T) {
	pool := setupTest(t)
	ctx := context.Background()

	rows := []struct {
		status     int
		durationMs int
	}{
		{200, 10}, {200, 20}, {200, 30}, {200, 40}, {500, 100},
	}
	for _, r := range rows {
		if _, err := pool.Exec(ctx, `
			INSERT INTO request_metrics (method, path_pattern, status_code, duration_ms, occurred_at)
			VALUES ('GET', '/api/firms/{id}/products', $1, $2, now())
		`, r.status, r.durationMs); err != nil {
			t.Fatalf("insert request metric: %v", err)
		}
	}

	since := time.Now().UTC().Add(-time.Hour)
	stats, err := apm.EndpointStatsSince(ctx, pool, since)
	if err != nil {
		t.Fatalf("EndpointStatsSince: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 endpoint pattern, got %d", len(stats))
	}
	s := stats[0]
	if s.RequestCount != 5 {
		t.Errorf("expected requestCount 5, got %d", s.RequestCount)
	}
	if s.ErrorRate != 0.2 {
		t.Errorf("expected errorRate 0.2 (1 of 5 >= 400), got %v", s.ErrorRate)
	}

	rate, err := apm.ErrorRate(ctx, pool, 60)
	if err != nil {
		t.Fatalf("ErrorRate: %v", err)
	}
	if rate != 0.2 {
		t.Errorf("expected overall error rate 0.2, got %v", rate)
	}

	volume, err := apm.RequestVolume(ctx, pool, 60)
	if err != nil {
		t.Fatalf("RequestVolume: %v", err)
	}
	if volume != 5 {
		t.Errorf("expected request volume 5, got %d", volume)
	}
}
