// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apm

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EndpointStats is one path_pattern's own aggregate response-time/error
// stats over a window - the shape both GET /api/platform-admin/metrics/summary
// (per-endpoint p50/p95/p99 and error rate) and TopSlowestEndpoints
// share.
type EndpointStats struct {
	PathPattern  string
	P50Ms        float64
	P95Ms        float64
	P99Ms        float64
	ErrorRate    float64
	RequestCount int64
}

// EndpointStatsSince returns one EndpointStats row per distinct
// path_pattern seen since since, most-recent-activity-agnostic (no
// ordering guarantee - callers that want "top slowest" order by P95Ms
// themselves, see TopSlowestEndpoints).
//
// percentile_cont (a real ordered-set aggregate Postgres computes
// server-side) is used for p50/p95/p99 rather than pulling every row's
// own duration_ms into Go and sorting there - the same "let Postgres do
// the numeric work" discipline internal/invoicing's own numeric ledger
// arithmetic already follows, just for percentiles instead of sums.
func EndpointStatsSince(ctx context.Context, pool *pgxpool.Pool, since time.Time) ([]EndpointStats, error) {
	rows, err := pool.Query(ctx, `
		SELECT
			path_pattern,
			percentile_cont(0.50) WITHIN GROUP (ORDER BY duration_ms),
			percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms),
			percentile_cont(0.99) WITHIN GROUP (ORDER BY duration_ms),
			count(*) FILTER (WHERE status_code >= 400)::float8 / count(*),
			count(*)
		FROM request_metrics
		WHERE occurred_at >= $1
		GROUP BY path_pattern
	`, since)
	if err != nil {
		return nil, fmt.Errorf("endpoint stats: %w", err)
	}
	defer rows.Close()

	var results []EndpointStats
	for rows.Next() {
		var s EndpointStats
		if err := rows.Scan(&s.PathPattern, &s.P50Ms, &s.P95Ms, &s.P99Ms, &s.ErrorRate, &s.RequestCount); err != nil {
			return nil, fmt.Errorf("scan endpoint stats row: %w", err)
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// TopSlowestEndpoints returns the top `limit` path_patterns by their own
// p95 response time since since - Part 1's own "top 10 slowest endpoint
// patterns" requirement, built directly on EndpointStatsSince's
// underlying query rather than a second, near-duplicate one (only the
// ORDER BY/LIMIT differ).
func TopSlowestEndpoints(ctx context.Context, pool *pgxpool.Pool, since time.Time, limit int) ([]EndpointStats, error) {
	rows, err := pool.Query(ctx, `
		SELECT
			path_pattern,
			percentile_cont(0.50) WITHIN GROUP (ORDER BY duration_ms),
			percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms),
			percentile_cont(0.99) WITHIN GROUP (ORDER BY duration_ms),
			count(*) FILTER (WHERE status_code >= 400)::float8 / count(*),
			count(*)
		FROM request_metrics
		WHERE occurred_at >= $1
		GROUP BY path_pattern
		ORDER BY percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) DESC
		LIMIT $2
	`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("top slowest endpoints: %w", err)
	}
	defer rows.Close()

	var results []EndpointStats
	for rows.Next() {
		var s EndpointStats
		if err := rows.Scan(&s.PathPattern, &s.P50Ms, &s.P95Ms, &s.P99Ms, &s.ErrorRate, &s.RequestCount); err != nil {
			return nil, fmt.Errorf("scan top slowest endpoint row: %w", err)
		}
		results = append(results, s)
	}
	return results, rows.Err()
}

// HourlyVolume is one hour's own total request count - Part 1's own
// "request volume by hour (last 24h)" requirement.
type HourlyVolume struct {
	Hour  time.Time
	Count int64
}

// VolumeByHourSince buckets request counts via date_trunc('hour', ...) -
// the same Postgres-native bucketing internal/analytics.TimeSeriesQuery
// already establishes for its own time-series endpoint, reused here
// rather than a bespoke Go-side bucketing loop.
func VolumeByHourSince(ctx context.Context, pool *pgxpool.Pool, since time.Time) ([]HourlyVolume, error) {
	rows, err := pool.Query(ctx, `
		SELECT date_trunc('hour', occurred_at), count(*)
		FROM request_metrics
		WHERE occurred_at >= $1
		GROUP BY 1
		ORDER BY 1
	`, since)
	if err != nil {
		return nil, fmt.Errorf("volume by hour: %w", err)
	}
	defer rows.Close()

	var results []HourlyVolume
	for rows.Next() {
		var v HourlyVolume
		if err := rows.Scan(&v.Hour, &v.Count); err != nil {
			return nil, fmt.Errorf("scan hourly volume row: %w", err)
		}
		results = append(results, v)
	}
	return results, rows.Err()
}

// ErrorRate is the overall (all endpoints combined) error rate over the
// trailing window minutes - internal/alerting's own 'error_rate' metric,
// and also usable directly by an operator/future dashboard for an
// installation-wide figure distinct from EndpointStatsSince's own
// per-endpoint breakdown.
func ErrorRate(ctx context.Context, pool *pgxpool.Pool, windowMinutes int) (float64, error) {
	var rate *float64
	err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status_code >= 400)::float8 / NULLIF(count(*), 0)
		FROM request_metrics
		WHERE occurred_at >= now() - ($1 * interval '1 minute')
	`, windowMinutes).Scan(&rate)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("error rate: %w", err)
	}
	if rate == nil {
		return 0, nil
	}
	return *rate, nil
}

// ResponseTimeP95 is the overall p95 response time (milliseconds) over
// the trailing window minutes - internal/alerting's own
// 'response_time_p95' metric.
func ResponseTimeP95(ctx context.Context, pool *pgxpool.Pool, windowMinutes int) (float64, error) {
	var p95 *float64
	err := pool.QueryRow(ctx, `
		SELECT percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms)
		FROM request_metrics
		WHERE occurred_at >= now() - ($1 * interval '1 minute')
	`, windowMinutes).Scan(&p95)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("response time p95: %w", err)
	}
	if p95 == nil {
		return 0, nil
	}
	return *p95, nil
}

// RequestVolume is the plain request count over the trailing window
// minutes - internal/alerting's own 'volume_drop' metric compares this
// window's own count against the equivalent prior window (see
// RequestVolumeBetween, and internal/alerting's own doc comment for how
// the comparison works).
func RequestVolume(ctx context.Context, pool *pgxpool.Pool, windowMinutes int) (int64, error) {
	var count int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM request_metrics
		WHERE occurred_at >= now() - ($1 * interval '1 minute')
	`, windowMinutes).Scan(&count); err != nil {
		return 0, fmt.Errorf("request volume: %w", err)
	}
	return count, nil
}

// RequestVolumeBetween is the plain request count within [from, to) -
// internal/alerting's own 'volume_drop' metric uses this to read the
// window immediately BEFORE the current one (RequestVolume's own
// trailing window), so a drop can be measured relative to the
// installation's own recent-past traffic rather than a hardcoded
// baseline.
func RequestVolumeBetween(ctx context.Context, pool *pgxpool.Pool, from, to time.Time) (int64, error) {
	var count int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM request_metrics
		WHERE occurred_at >= $1 AND occurred_at < $2
	`, from, to).Scan(&count); err != nil {
		return 0, fmt.Errorf("request volume between: %w", err)
	}
	return count, nil
}
