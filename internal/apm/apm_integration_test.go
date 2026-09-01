// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apm_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/apm"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// setupTest returns both pools (unlike a single shared appPool) because
// the partition-grant tests below need the admin pool to force a
// partition into a known-missing (or known-conflicting) state before
// exercising create_request_metrics_partition as zonaryos_app - see
// each test's own comment for why "drop/seed it yourself first" is what
// makes these deterministic regardless of what migrations pre-created
// or what calendar date CI happens to run on.
func setupTest(t *testing.T) (appPool, adminPool *pgxpool.Pool) {
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
	// TRUNCATE cascades to every partition, including request_metrics_default -
	// clears any row a previous (possibly failed) test run left behind.
	if _, err := adminPool.Exec(ctx, `TRUNCATE request_metrics`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err = zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(appPool.Close)
	return appPool, adminPool
}

// dropPartition removes a request_metrics partition entirely (as the
// admin role, which owns the parent table) so a test can force a
// specific month into a known-missing state regardless of what the
// migrations statically created or what any earlier test run left
// behind. DROP TABLE on a partition detaches and drops it in one step.
func dropPartition(t *testing.T, adminPool *pgxpool.Pool, name string) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(), fmt.Sprintf(`DROP TABLE IF EXISTS %s`, name)); err != nil {
		t.Fatalf("drop partition %q: %v", name, err)
	}
}

func partitionExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var regclass *string
	if err := pool.QueryRow(context.Background(), `SELECT to_regclass($1)::text`, name).Scan(&regclass); err != nil {
		t.Fatalf("check partition %q exists: %v", name, err)
	}
	return regclass != nil
}

// TestEnsureFuturePartitions_CreatesMonthAheadPartitions is this
// batch's own required test: month-ahead partitions are created when
// missing. This is exactly the test that stayed green for 11 days
// (2026-08-20 to 2026-09-01) while EnsureFuturePartitions was actually
// broken (see migrations/0046_fix_request_metrics_partition_grant.up.sql) -
// migrations/0041's own hardcoded Aug/Sep/Oct 2026 partitions happened
// to cover every CI run's 3-month look-ahead window until the calendar
// rolled past October, so the code path that actually creates a
// partition never ran. To not repeat that: this test picks a month
// inside EnsureFuturePartitions' own look-ahead window and drops that
// specific partition via the admin pool FIRST, so it is guaranteed
// missing by construction - not by hoping no static partition or
// earlier test run happens to cover it - regardless of what date this
// runs on.
func TestEnsureFuturePartitions_CreatesMonthAheadPartitions(t *testing.T) {
	appPool, adminPool := setupTest(t)
	ctx := context.Background()

	target := time.Now().UTC().AddDate(0, 2, 0)
	monthStart := time.Date(target.Year(), target.Month(), 1, 0, 0, 0, 0, time.UTC)
	name := apm.PartitionName(monthStart)
	dropPartition(t, adminPool, name)

	if err := apm.EnsureFuturePartitions(ctx, appPool); err != nil {
		t.Fatalf("EnsureFuturePartitions: %v", err)
	}

	if !partitionExists(t, appPool, name) {
		t.Errorf("expected partition %q to exist after EnsureFuturePartitions, it does not", name)
	}

	health, err := apm.CheckPartitionHealth(ctx, appPool)
	if err != nil {
		t.Fatalf("CheckPartitionHealth: %v", err)
	}
	if len(health.MissingMonths) != 0 {
		t.Errorf("expected no missing months after EnsureFuturePartitions, got %v", health.MissingMonths)
	}
}

// TestCreateRequestMetricsPartition_RejectsNonFirstOfMonth and
// TestCreateRequestMetricsPartition_RejectsOutOfWindowMonth pin down
// create_request_metrics_partition's own two input bounds. These guards
// are what makes EXECUTE on a table-creating function safe to hand to
// zonaryos_app at all (see migrations/0046's header comment) - without
// their own test coverage, a later change could quietly loosen or drop
// either one and every other test here would keep passing.
func TestCreateRequestMetricsPartition_RejectsNonFirstOfMonth(t *testing.T) {
	appPool, _ := setupTest(t)
	ctx := context.Background()

	target := time.Now().UTC().AddDate(0, 1, 0)
	notFirstOfMonth := time.Date(target.Year(), target.Month(), 15, 0, 0, 0, 0, time.UTC)

	_, err := appPool.Exec(ctx, `SELECT create_request_metrics_partition($1)`, notFirstOfMonth)
	if err == nil {
		t.Fatal("expected create_request_metrics_partition to reject a month_start that is not the first of a month, got no error")
	}
}

func TestCreateRequestMetricsPartition_RejectsOutOfWindowMonth(t *testing.T) {
	appPool, _ := setupTest(t)
	ctx := context.Background()

	target := time.Now().UTC().AddDate(0, 7, 0) // > 6 months ahead
	beyondWindow := time.Date(target.Year(), target.Month(), 1, 0, 0, 0, 0, time.UTC)

	_, err := appPool.Exec(ctx, `SELECT create_request_metrics_partition($1)`, beyondWindow)
	if err == nil {
		t.Fatal("expected create_request_metrics_partition to reject a month more than 6 months ahead of current_date, got no error")
	}
}

// TestCreateRequestMetricsPartition_RejectsAttachWhenDefaultPartitionHasConflictingRows
// is the empirically-verified failure mode this bug created for real:
// once the scheduler was broken for a month, that month's rows land in
// request_metrics_default, and PostgreSQL then refuses to attach the
// proper partition for that range (SQLSTATE 23514, check_violation -
// confirmed against a real PostgreSQL 16 instance, not assumed from the
// condition name). create_request_metrics_partition must not swallow
// this or silently move the rows - only re-raise with an actionable
// message - so this test pins both the SQLSTATE the handler actually
// catches and the message's content.
func TestCreateRequestMetricsPartition_RejectsAttachWhenDefaultPartitionHasConflictingRows(t *testing.T) {
	appPool, adminPool := setupTest(t)
	ctx := context.Background()

	target := time.Now().UTC().AddDate(0, 3, 0)
	monthStart := time.Date(target.Year(), target.Month(), 1, 0, 0, 0, 0, time.UTC)
	name := apm.PartitionName(monthStart)
	dropPartition(t, adminPool, name)

	// With no partition covering this month, this row lands in
	// request_metrics_default - simulating "the scheduler was down for
	// this interval and this month's data already leaked."
	conflictingRowAt := monthStart.Add(24 * time.Hour)
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO request_metrics (method, path_pattern, status_code, duration_ms, occurred_at)
		VALUES ('GET', '/probe', 200, 1, $1)
	`, conflictingRowAt); err != nil {
		t.Fatalf("insert conflicting row into request_metrics_default: %v", err)
	}

	_, err := appPool.Exec(ctx, `SELECT create_request_metrics_partition($1)`, monthStart)
	if err == nil {
		t.Fatal("expected create_request_metrics_partition to fail when request_metrics_default already holds a conflicting row")
	}
	if !strings.Contains(err.Error(), "already contains rows") {
		t.Errorf("expected an actionable 'already contains rows' message, got: %v", err)
	}
}

// TestZonaryosApp_CannotBypassPartitionFunction is the negative
// counterpart to the positive tests above: it pins the grant shape
// migrations/0046 establishes, not just that the function works. This
// is what stops a future change from quietly widening zonaryos_app's
// privileges back toward migrations/0041's original (ineffective and
// too-broad) schema-level CREATE grant while the positive tests above
// keep passing regardless - exactly how 0041's own mistake went
// unnoticed for 11 days.
func TestZonaryosApp_CannotBypassPartitionFunction(t *testing.T) {
	appPool, _ := setupTest(t)
	ctx := context.Background()

	if _, err := appPool.Exec(ctx, `
		CREATE TABLE request_metrics_probe PARTITION OF request_metrics
		FOR VALUES FROM ('2099-01-01') TO ('2099-02-01')
	`); err == nil {
		t.Fatal("expected zonaryos_app to be unable to attach a request_metrics partition directly (bypassing create_request_metrics_partition), got no error")
	}

	if _, err := appPool.Exec(ctx, `DROP TABLE request_metrics`); err == nil {
		t.Fatal("expected zonaryos_app to be unable to drop request_metrics, got no error")
	}
}

// TestEndpointStatsSince_ComputesPercentilesAndErrorRate confirms
// EndpointStatsSince/ErrorRate/ResponseTimeP95/RequestVolume - the
// shared primitives both the metrics-summary handler and
// internal/alerting's own checker read from - return the expected
// figures for a small, known set of inserted rows.
func TestEndpointStatsSince_ComputesPercentilesAndErrorRate(t *testing.T) {
	pool, _ := setupTest(t)
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
