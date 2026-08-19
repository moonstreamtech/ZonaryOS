// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package analytics_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/analytics"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run analytics integration tests")
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
		TRUNCATE firms, users, roles, role_permissions, user_firm_roles, analytics_events, audit_log, permissions CASCADE
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

// TestIngestEvents_StoresBatchCorrectly is Part 5's own required test:
// a batch of events is stored correctly.
func TestIngestEvents_StoresBatchCorrectly(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Ingest Firm", "ingest-owner")

	err := analytics.IngestEvents(ctx, appPool, firmID, userID, []analytics.EventInput{
		{EventType: "page_view", Properties: map[string]any{"page": "/dashboard"}},
		{EventType: "workflow_executed", Properties: map[string]any{"definitionKey": "stock_to_sale"}},
	})
	if err != nil {
		t.Fatalf("IngestEvents: %v", err)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT COUNT(*) FROM analytics_events WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 stored events, got %d", count)
	}

	var storedUserID uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT user_id FROM analytics_events WHERE firm_id = $1 AND event_type = 'page_view'`, firmID).Scan(&storedUserID); err != nil {
		t.Fatalf("read stored event user_id: %v", err)
	}
	if storedUserID != userID {
		t.Fatalf("expected stored event to be stamped with the caller's own userID %v, got %v", userID, storedUserID)
	}
}

// TestIngestEvents_Max100Enforced is Part 5's own required test: a batch
// over 100 events is rejected outright, not silently truncated.
func TestIngestEvents_Max100Enforced(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Max Batch Firm", "max-batch-owner")

	events := make([]analytics.EventInput, 101)
	for i := range events {
		events[i] = analytics.EventInput{EventType: "page_view"}
	}
	if err := analytics.IngestEvents(ctx, appPool, firmID, userID, events); !errors.Is(err, analytics.ErrInvalidEventBatch) {
		t.Fatalf("expected ErrInvalidEventBatch for a 101-event batch, got %v", err)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT COUNT(*) FROM analytics_events WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected zero events stored after a rejected over-cap batch, got %d", count)
	}

	if err := analytics.IngestEvents(ctx, appPool, firmID, userID, nil); !errors.Is(err, analytics.ErrInvalidEventBatch) {
		t.Fatalf("expected ErrInvalidEventBatch for an empty batch, got %v", err)
	}
}

// TestTimeSeriesQuery_DateTruncBucketingProducesCorrectCounts is Part 5's
// own required test: date_trunc bucketing produces correct counts for
// known data.
func TestTimeSeriesQuery_DateTruncBucketingProducesCorrectCounts(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Time Series Firm", "timeseries-owner")

	day1 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	events := []analytics.EventInput{
		{EventType: "workflow_executed", OccurredAt: ptrTime(day1)},
		{EventType: "workflow_executed", OccurredAt: ptrTime(day1.Add(2 * time.Hour))},
		{EventType: "workflow_executed", OccurredAt: ptrTime(day2)},
		{EventType: "page_view", OccurredAt: ptrTime(day1)}, // different event_type - must not count
	}
	if err := analytics.IngestEvents(ctx, appPool, firmID, userID, events); err != nil {
		t.Fatalf("IngestEvents: %v", err)
	}

	buckets, err := analytics.TimeSeriesQuery(ctx, appPool, firmID, userID, analytics.TimeSeriesQuerySpec{
		EventType: "workflow_executed", GroupBy: analytics.GroupByDay,
		From: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("TimeSeriesQuery: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("expected 2 day-buckets, got %+v", buckets)
	}
	if buckets[0].Count != 2 {
		t.Fatalf("expected day 1 bucket count 2, got %d", buckets[0].Count)
	}
	if buckets[1].Count != 1 {
		t.Fatalf("expected day 2 bucket count 1, got %d", buckets[1].Count)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
