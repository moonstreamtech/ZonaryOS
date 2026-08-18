// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package activitylog_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/plugin"
	"github.com/moonstreamtech/ZonaryOS/internal/plugins/activitylog"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise internal/plugins/activitylog against a real
// Postgres instance, same convention as every other package's
// integration tests in this codebase. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL
// are set.

func setupTest(t *testing.T) (adminPool, appPool *pgxpool.Pool) {
	t.Helper()
	adminDSN := os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN := os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run activitylog integration tests")
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
	if _, err := adminPool.Exec(ctx, `TRUNCATE firms, activity_log CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err = zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(appPool.Close)

	return adminPool, appPool
}

func seedFirm(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ($1) RETURNING id`, name).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	return firmID
}

// TestOnTransition_CreatesActivityLogEntryWithRightDescription is Part 3's
// own explicit test requirement: transition event creates an activity log
// entry with the right description.
func TestOnTransition_CreatesActivityLogEntryWithRightDescription(t *testing.T) {
	_, appPool := setupTest(t)
	ctx := context.Background()
	firmID := seedFirm(ctx, t, appPool, "Firm A")

	p := activitylog.New(appPool)
	instanceID := uuid.New()
	event := plugin.TransitionEvent{
		FirmID: firmID, InstanceID: instanceID, DefinitionKey: "stock_to_sale",
		FromState: "pending", ToState: "sold", ActionKey: "sell", Payload: map[string]any{"quantity": 5},
	}
	if err := p.OnTransition(ctx, event); err != nil {
		t.Fatalf("OnTransition: %v", err)
	}

	entries, err := activitylog.ListToday(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("ListToday: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 activity log entry, got %d", len(entries))
	}
	const wantDescription = "stock_to_sale: pending -> sold (sell)"
	if entries[0].Description != wantDescription {
		t.Fatalf("expected description %q, got %q", wantDescription, entries[0].Description)
	}
	if entries[0].SourceInstanceID == nil || *entries[0].SourceInstanceID != instanceID {
		t.Fatalf("expected source_instance_id to be set to the transition's own instance id, got %v", entries[0].SourceInstanceID)
	}
}

func TestGetKPIs_CountsTodaysEntries(t *testing.T) {
	_, appPool := setupTest(t)
	ctx := context.Background()
	firmID := seedFirm(ctx, t, appPool, "Firm A")

	p := activitylog.New(appPool)
	for i := 0; i < 3; i++ {
		event := plugin.TransitionEvent{FirmID: firmID, InstanceID: uuid.New(), DefinitionKey: "def", FromState: "a", ToState: "b", ActionKey: "go"}
		if err := p.OnTransition(ctx, event); err != nil {
			t.Fatalf("OnTransition: %v", err)
		}
	}

	kpis, err := p.GetKPIs(ctx, firmID, appPool)
	if err != nil {
		t.Fatalf("GetKPIs: %v", err)
	}
	if len(kpis) != 1 {
		t.Fatalf("expected exactly 1 KPI tile, got %d", len(kpis))
	}
	if kpis[0].Key != "activity_count_today" || kpis[0].Value != "3" {
		t.Fatalf("expected activity_count_today=3, got %+v", kpis[0])
	}
}

func TestGetKPIs_NoEntriesReadsBackAsZero(t *testing.T) {
	_, appPool := setupTest(t)
	ctx := context.Background()
	firmID := seedFirm(ctx, t, appPool, "Firm A")

	p := activitylog.New(appPool)
	kpis, err := p.GetKPIs(ctx, firmID, appPool)
	if err != nil {
		t.Fatalf("GetKPIs: %v", err)
	}
	if len(kpis) != 1 || kpis[0].Value != "0" {
		t.Fatalf("expected activity_count_today=0 for a firm with no entries, got %+v", kpis)
	}
}
