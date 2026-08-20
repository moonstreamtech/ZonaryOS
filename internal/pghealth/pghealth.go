// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package pghealth is a small, dependency-free set of read-only queries
// against Postgres's own system catalogs/statistics views
// (pg_stat_activity, pg_stat_user_tables) - no new tables of its own.
// It exists as its own package (rather than living inside
// internal/health or internal/apm directly) because both of those
// packages need it: internal/health's GET /health?detail=true
// (performance monitoring/alerting/operational excellence batch, Part
// 3) and internal/apm's self-healing scheduler (same batch, Part 4's
// stale-lock detection) both read the same underlying views for two
// different purposes, and duplicating these queries in both packages
// would be the same kind of avoidable duplication
// internal/analytics.TopByProperty's own doc comment already argues
// against for its "top workflows"/page-view-heatmap primitive.
//
// Every query here is read-only against a built-in system view - no
// table this package owns, nothing to migrate. Reading other backends'
// own pg_stat_activity.query/query_start columns requires the pg_monitor
// built-in role (migrations/0041_performance_monitoring.up.sql grants it
// to zonaryos_app) - without it, Postgres silently returns NULL for
// those two columns on any backend that isn't the calling role itself.
package pghealth

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ActiveConnectionCount is the total number of connections Postgres
// itself currently has open (pg_stat_activity has one row per backend,
// including this very query's own connection) - a coarse, whole-instance
// signal, not scoped to this application's own pool alone (a wedged
// connection from an entirely different client would still show up
// here, which is exactly what an operator diagnosing "why is Postgres
// refusing new connections" wants to see).
func ActiveConnectionCount(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active connections: %w", err)
	}
	return count, nil
}

// OldestRunningQuerySeconds returns how long the single longest-running
// still-active query has been running, or nil if no query is currently
// active (every backend is idle) - the same "nil means no data, not a
// misleading zero" distinction internal/reports/cohort.go's own
// CohortRow.Periods already establishes: an idle instance and an
// instance where the single active query has been running for
// literally 0 seconds are genuinely different states.
func OldestRunningQuerySeconds(ctx context.Context, pool *pgxpool.Pool) (*float64, error) {
	var seconds *float64
	err := pool.QueryRow(ctx, `
		SELECT extract(epoch FROM (now() - query_start))
		FROM pg_stat_activity
		WHERE state = 'active' AND pid != pg_backend_pid()
		ORDER BY query_start ASC
		LIMIT 1
	`).Scan(&seconds)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("oldest running query: %w", err)
	}
	return seconds, nil
}

// TableBloat is one table's own rough dead-tuple bloat estimate - not a
// precise vacuum-worthy calculation (that needs pgstattuple or a real
// VACUUM ANALYZE pass), just the same cheap, always-available
// n_dead_tup/(n_live_tup+n_dead_tup) ratio every operator already
// reaches for as a first signal from pg_stat_user_tables, the built-in
// view every Postgres install already maintains with no extra
// extension.
type TableBloat struct {
	TableName    string
	LiveTuples   int64
	DeadTuples   int64
	DeadRatio    float64
	TotalSizeStr string
}

// TableBloatEstimate returns the top limit largest tables (by total
// on-disk size, indexes included - pg_total_relation_size) and each
// one's own dead-tuple ratio.
func TableBloatEstimate(ctx context.Context, pool *pgxpool.Pool, limit int) ([]TableBloat, error) {
	rows, err := pool.Query(ctx, `
		SELECT
			relname,
			n_live_tup,
			n_dead_tup,
			CASE WHEN (n_live_tup + n_dead_tup) > 0
				THEN n_dead_tup::float8 / (n_live_tup + n_dead_tup)
				ELSE 0
			END AS dead_ratio,
			pg_size_pretty(pg_total_relation_size(relid))
		FROM pg_stat_user_tables
		ORDER BY pg_total_relation_size(relid) DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("table bloat estimate: %w", err)
	}
	defer rows.Close()

	var results []TableBloat
	for rows.Next() {
		var b TableBloat
		if err := rows.Scan(&b.TableName, &b.LiveTuples, &b.DeadTuples, &b.DeadRatio, &b.TotalSizeStr); err != nil {
			return nil, fmt.Errorf("scan table bloat row: %w", err)
		}
		results = append(results, b)
	}
	return results, rows.Err()
}

// PartitionHealth reports whether tableName (a PARTITION BY RANGE table
// following internal/apm's own request_metrics_YYYY_MM naming
// convention) has a real partition for each of the next monthsAhead
// months, INCLUDING the current one - the same check internal/apm's
// self-healing scheduler runs to decide whether it needs to create one.
type PartitionHealth struct {
	// MissingMonths is every "YYYY-MM" month (current month first) that
	// does NOT yet have its own partition - empty means fully healthy.
	MissingMonths []string
}

// CheckPartitionHealth uses to_regclass, not a catalog join, so a
// missing partition is a plain nil result rather than a query error -
// the same "absence is a normal, first-class outcome" reasoning
// OldestRunningQuerySeconds' own nil return follows.
func CheckPartitionHealth(ctx context.Context, pool *pgxpool.Pool, partitionNameFor func(t time.Time) string, monthsAhead int) (PartitionHealth, error) {
	now := time.Now().UTC()
	var missing []string
	for i := 0; i < monthsAhead; i++ {
		month := time.Date(now.Year(), now.Month()+time.Month(i), 1, 0, 0, 0, 0, time.UTC)
		name := partitionNameFor(month)
		var regclass *string
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1)::text`, name).Scan(&regclass); err != nil {
			return PartitionHealth{}, fmt.Errorf("check partition %q: %w", name, err)
		}
		if regclass == nil {
			missing = append(missing, month.Format("2006-01"))
		}
	}
	return PartitionHealth{MissingMonths: missing}, nil
}

// StaleQuery is one currently-active query that has been running longer
// than a caller-supplied threshold - Part 4's own "stale lock detection"
// (monitoring-only, never auto-killed).
type StaleQuery struct {
	PID              int32
	RunningSeconds   float64
	QueryFingerprint string
}

// queryFingerprintMaxLen is Part 4's own "first 100 chars, no parameter
// values" bound. Truncating to a fixed length is the entire "no
// parameter values" guarantee this needs: this codebase's own database
// layer (pgx) always issues parameterized queries over the extended
// query protocol, so pg_stat_activity.query already holds the submitted
// SQL text with literal $1/$2/... placeholders, never the bound values
// themselves - Postgres does not inline parameter values into this
// column for an extended-protocol query, so there is no separate
// redaction step needed beyond bounding the length for a readable log
// line.
const queryFingerprintMaxLen = 100

// StaleQueries returns every currently-active query that has been
// running for longer than threshold, for internal/apm's self-healing
// scheduler to log (slog.Warn) - monitoring only, this package never
// terminates a backend.
func StaleQueries(ctx context.Context, pool *pgxpool.Pool, threshold time.Duration) ([]StaleQuery, error) {
	rows, err := pool.Query(ctx, `
		SELECT pid, extract(epoch FROM (now() - query_start)), left(coalesce(query, ''), $1)
		FROM pg_stat_activity
		WHERE state = 'active' AND pid != pg_backend_pid()
		  AND query_start <= now() - $2::interval
	`, queryFingerprintMaxLen, fmt.Sprintf("%d seconds", int(threshold.Seconds())))
	if err != nil {
		return nil, fmt.Errorf("stale queries: %w", err)
	}
	defer rows.Close()

	var results []StaleQuery
	for rows.Next() {
		var sq StaleQuery
		if err := rows.Scan(&sq.PID, &sq.RunningSeconds, &sq.QueryFingerprint); err != nil {
			return nil, fmt.Errorf("scan stale query row: %w", err)
		}
		results = append(results, sq)
	}
	return results, rows.Err()
}
