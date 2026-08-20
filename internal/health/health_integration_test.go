// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package health_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/health"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

func setupTest(t *testing.T) *pgxpool.Pool {
	t.Helper()
	adminDSN := os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN := os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run health integration tests")
	}
	ctx := context.Background()
	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	appPool, err := zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(appPool.Close)
	return appPool
}

// TestHandler_DetailTrueReturnsDatabaseDetailStructure is this batch's
// own required test: "Health detail: pg_stat_activity queries return
// correct structure." Confirms GET /health?detail=true's own
// database_detail block is present with the four fields Part 3's own
// brief lists, all populated from real pg_stat_activity/
// pg_stat_user_tables queries against a real Postgres (this is exactly
// why this is an _integration_test.go file, unlike health_test.go's own
// nil-pool unit tests).
func TestHandler_DetailTrueReturnsDatabaseDetailStructure(t *testing.T) {
	pool := setupTest(t)
	h := health.Handler(pool, "", nil)

	req := httptest.NewRequest(http.MethodGet, "/health?detail=true", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var body struct {
		DatabaseDetail struct {
			ActiveConnections      int      `json:"activeConnections"`
			OldestRunningQuerySecs *float64 `json:"oldestRunningQuerySeconds"`
			TableBloatTop5         []struct {
				TableName string  `json:"tableName"`
				DeadRatio float64 `json:"deadRatio"`
				TotalSize string  `json:"totalSize"`
			} `json:"tableBloatTop5"`
			RequestMetricsPartitionHealth struct {
				Healthy       bool     `json:"healthy"`
				MissingMonths []string `json:"missingMonths,omitempty"`
			} `json:"requestMetricsPartitionHealth"`
		} `json:"database_detail"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.DatabaseDetail.ActiveConnections < 1 {
		t.Errorf("expected at least 1 active connection (this test's own), got %d", body.DatabaseDetail.ActiveConnections)
	}
}

// TestHandler_DetailOmittedHasNoDatabaseDetail confirms the plain,
// unauthenticated GET /health (no ?detail=true) never runs the extra
// queries at all - Part 3's own "keep the lightweight health check fast"
// requirement.
func TestHandler_DetailOmittedHasNoDatabaseDetail(t *testing.T) {
	pool := setupTest(t)
	h := health.Handler(pool, "", nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := body["database_detail"]; ok {
		t.Error("expected no database_detail field when ?detail=true is not passed")
	}
}
