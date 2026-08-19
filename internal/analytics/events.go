// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package analytics is Parts 1-2 of the analytics/BI/advanced reporting
// batch: server-side event ingestion (analytics_events, migrations/0040 -
// see that migration's own doc comment for the declarative-partitioning
// design) and time-series/aggregate queries over it. Part 3 (cohort
// analysis) lives in internal/reports instead - it extends that
// package's own existing report engine and HTTP surface
// (/api/firms/{firmID}/reports/...), not this one. Part 4 (scheduled
// reports) lives in internal/scheduledreports - see that package's own
// doc comment for why it's a third package rather than folding into
// either internal/reports or internal/notification.
package analytics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// maxEventBatchSize is Part 1's own "max 100 per request" cap.
const maxEventBatchSize = 100

// EventInput is one event in an IngestEvents batch. UserID is NOT part
// of this input - the caller can't report an event "as" a different
// user; IngestEvents always stamps the authenticated caller's own
// userID (see that function's own doc comment).
type EventInput struct {
	EventType  string
	EntityType string
	EntityID   *uuid.UUID
	Properties map[string]any
	// OccurredAt, if non-nil, lets a client report the time an event
	// actually happened client-side (network delay/offline queueing
	// means "when the request arrived" isn't always "when it happened").
	// nil means "now, server-side" - the common case.
	OccurredAt *time.Time
}

// IngestEvents stores a batch of analytics events for firmID - member-
// gated (any authenticated firm member can report their own usage;
// analytics ingestion isn't a structural firm decision the way defining
// a report is). Rejects an empty or over-100 batch outright rather than
// silently truncating - a caller sending garbage should see a 400, not a
// partially-applied write. Every event is stamped with the caller's own
// userID, never a client-supplied one.
func IngestEvents(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, events []EventInput) error {
	if len(events) == 0 || len(events) > maxEventBatchSize {
		return fmt.Errorf("%w: batch must contain 1-%d events, got %d", ErrInvalidEventBatch, maxEventBatchSize, len(events))
	}
	for _, e := range events {
		if strings.TrimSpace(e.EventType) == "" {
			return fmt.Errorf("%w: eventType must not be empty", ErrInvalidEventBatch)
		}
	}

	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		batch := &pgx.Batch{}
		for _, e := range events {
			occurredAt := time.Now().UTC()
			if e.OccurredAt != nil {
				occurredAt = *e.OccurredAt
			}
			properties := e.Properties
			if properties == nil {
				properties = map[string]any{}
			}
			var entityType *string
			if strings.TrimSpace(e.EntityType) != "" {
				entityType = &e.EntityType
			}
			batch.Queue(`
				INSERT INTO analytics_events (firm_id, event_type, entity_type, entity_id, user_id, properties, occurred_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, firmID, strings.TrimSpace(e.EventType), entityType, e.EntityID, userID, properties, occurredAt)
		}
		br := tx.SendBatch(ctx, batch)
		defer br.Close()
		for range events {
			if _, err := br.Exec(); err != nil {
				return fmt.Errorf("insert analytics event: %w", err)
			}
		}
		return nil
	})
}

// TimeSeriesGroupBy is the closed bucket-width vocabulary Part 2's own
// query spec accepts - passed straight through to Postgres's own
// date_trunc as a bound parameter (not string-concatenated), but still
// validated against this allow-list first so an unsupported value is a
// clear 400 rather than whatever error date_trunc itself would raise.
type TimeSeriesGroupBy string

const (
	GroupByHour  TimeSeriesGroupBy = "hour"
	GroupByDay   TimeSeriesGroupBy = "day"
	GroupByWeek  TimeSeriesGroupBy = "week"
	GroupByMonth TimeSeriesGroupBy = "month"
)

func validGroupBy(g TimeSeriesGroupBy) bool {
	switch g {
	case GroupByHour, GroupByDay, GroupByWeek, GroupByMonth:
		return true
	default:
		return false
	}
}

// PropertyFilter is one {property, value} equality filter against an
// event's own properties jsonb - property is bound as a parameter into
// `properties ->> $N`, never string-concatenated into the query, so an
// arbitrary caller-supplied key is safe (it can only ever select a jsonb
// field by name, never alter the query's own shape).
type PropertyFilter struct {
	Property string
	Value    string
}

// TimeSeriesQuerySpec is Part 2's own POST body shape.
type TimeSeriesQuerySpec struct {
	EventType string
	GroupBy   TimeSeriesGroupBy
	From      time.Time
	To        time.Time
	Filters   []PropertyFilter
}

// TimeSeriesBucket is one row of TimeSeriesQuery's own result.
type TimeSeriesBucket struct {
	Bucket time.Time
	Count  int
}

// TimeSeriesQuery buckets firmID's analytics_events by spec.GroupBy via
// Postgres's own date_trunc, counting events per bucket - the foundation
// for a trend chart. Member-gated, same tier as IngestEvents.
func TimeSeriesQuery(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, spec TimeSeriesQuerySpec) ([]TimeSeriesBucket, error) {
	if strings.TrimSpace(spec.EventType) == "" {
		return nil, fmt.Errorf("%w: eventType must not be empty", ErrInvalidQuery)
	}
	if !validGroupBy(spec.GroupBy) {
		return nil, fmt.Errorf("%w: unsupported groupBy %q", ErrInvalidQuery, spec.GroupBy)
	}
	if spec.From.IsZero() || spec.To.IsZero() || !spec.From.Before(spec.To) {
		return nil, fmt.Errorf("%w: from must be a valid timestamp before to", ErrInvalidQuery)
	}

	var buckets []TimeSeriesBucket
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		args := []any{firmID, spec.EventType, string(spec.GroupBy), spec.From, spec.To}
		query := `
			SELECT date_trunc($3, occurred_at) AS bucket, COUNT(*)
			FROM analytics_events
			WHERE firm_id = $1 AND event_type = $2 AND occurred_at >= $4 AND occurred_at < $5`
		for _, f := range spec.Filters {
			args = append(args, f.Property, f.Value)
			query += fmt.Sprintf(" AND properties ->> $%d = $%d", len(args)-1, len(args))
		}
		query += " GROUP BY bucket ORDER BY bucket"

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("time series query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var b TimeSeriesBucket
			if err := rows.Scan(&b.Bucket, &b.Count); err != nil {
				return err
			}
			buckets = append(buckets, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return buckets, nil
}

// PropertyCount is one row of TopByProperty's own result - a distinct
// properties[key] value and how many matching events carried it.
type PropertyCount struct {
	Value string
	Count int
}

// TopByProperty groups firmID's analytics_events (matching eventType)
// by one properties jsonb key, ordered by count descending - the shared
// primitive behind both "top workflows by execution count"
// (eventType="workflow_executed", property="definitionKey") and the page
// view heatmap table (eventType="page_view", property="page"). Member-
// gated, same tier as IngestEvents.
func TopByProperty(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, eventType, property string, limit int) ([]PropertyCount, error) {
	if strings.TrimSpace(eventType) == "" || strings.TrimSpace(property) == "" {
		return nil, fmt.Errorf("%w: eventType and property must not be empty", ErrInvalidQuery)
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	var counts []PropertyCount
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT properties ->> $3 AS value, COUNT(*)
			FROM analytics_events
			WHERE firm_id = $1 AND event_type = $2 AND properties ? $3
			GROUP BY value
			ORDER BY COUNT(*) DESC
			LIMIT $4
		`, firmID, eventType, property, limit)
		if err != nil {
			return fmt.Errorf("top by property query: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var c PropertyCount
			if err := rows.Scan(&c.Value, &c.Count); err != nil {
				return err
			}
			counts = append(counts, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return counts, nil
}

// ActiveUserCount returns the number of distinct user_ids that fired at
// least one analytics event in the last sinceDays days - "active users"
// on the /analytics dashboard. Member-gated, same tier as IngestEvents.
func ActiveUserCount(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, sinceDays int) (int, error) {
	if sinceDays <= 0 {
		sinceDays = 30
	}

	var count int
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		return tx.QueryRow(ctx, `
			SELECT COUNT(DISTINCT user_id)
			FROM analytics_events
			WHERE firm_id = $1 AND user_id IS NOT NULL AND occurred_at >= now() - make_interval(days => $2)
		`, firmID, sinceDays).Scan(&count)
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
