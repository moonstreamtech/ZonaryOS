// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package dbtest is a small collection of Postgres test helpers shared
// across this codebase's own `_integration_test.go` files - split out of
// internal/platform/db itself (rather than living there directly) so
// that importing it never pulls the stdlib `testing` package into
// production code (cmd/server and friends never import this package).
package dbtest

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ExplainAnalyze runs `EXPLAIN (ANALYZE, FORMAT TEXT) query` with args
// against pool and returns the plan as a single string, logging it via
// t.Log along the way - not run automatically by any test in this
// codebase's suite (that would slow down every CI run for no benefit),
// but available for a developer to call directly from a test while
// investigating a specific query's performance (an index that isn't
// being used, a sequential scan that should be an index scan, ...).
// Failing to EXPLAIN a query fails the test via t.Fatalf, the same "a
// broken assumption in the test itself is a test failure" convention
// every other helper in this codebase's own `_integration_test.go` files
// (e.g. reports_test.setupTest) already follows.
func ExplainAnalyze(t *testing.T, pool *pgxpool.Pool, query string, args ...any) string {
	t.Helper()
	rows, err := pool.Query(context.Background(), "EXPLAIN (ANALYZE, FORMAT TEXT) "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN ANALYZE: %v", err)
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("EXPLAIN ANALYZE scan: %v", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("EXPLAIN ANALYZE: %v", err)
	}

	plan := strings.Join(lines, "\n")
	t.Logf("EXPLAIN ANALYZE %s\n%s", query, plan)
	return plan
}
