// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package apm is Parts 1 and 4 of the performance monitoring, alerting,
// and operational excellence batch: request-level metrics collection
// (request_metrics, migrations/0041 - see that migration's own doc
// comment for the partitioning design) and the self-healing scheduler
// that keeps its partitions ahead of the data. Part 2 (alerting) lives
// in internal/alerting instead, which imports this package's own
// aggregate-query helpers (ErrorRate/ResponseTimeP95/RequestVolume)
// rather than duplicating the SQL behind them - the same
// internal/analytics.TopByProperty-style "one primitive, two callers"
// reasoning already established elsewhere in this codebase. Part 3
// (GET /health?detail=true) lives in internal/health, built on the
// shared internal/pghealth read-only queries this package also uses for
// its own stale-lock detection.
package apm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Record is one buffered, not-yet-flushed request metric.
type Record struct {
	Method      string
	PathPattern string
	StatusCode  int
	DurationMs  int
	// RequestFirmID is nil for an unauthenticated/non-firm-scoped
	// request (see ExtractFirmID) - request_metrics.request_firm_id is
	// nullable for exactly this reason.
	RequestFirmID *uuid.UUID
	OccurredAt    time.Time
}

// maxBufferSize/flushInterval are Part 1's own "buffer in memory, flush
// every 30 seconds or every 100 records, whichever comes first" design
// brief.
const (
	maxBufferSize = 100
	flushInterval = 30 * time.Second
)

// Buffer is a goroutine-safe, in-memory accumulator of not-yet-written
// request_metrics rows - the mechanism that lets Record (called from
// every single HTTP request, see internal/platform/middleware.RequestID's
// own MetricsRecorder hook) avoid a database round trip per request.
//
// Goroutine safety: a single sync.Mutex guards the buffered slice
// itself. Record's own critical section is a plain append plus a length
// check - no I/O happens while the lock is held. When the length check
// trips the 100-record threshold, the accumulated slice is swapped out
// for a fresh empty one (the classic double-buffering trick: the actual
// database write happens on the DETACHED old slice, entirely outside
// the mutex) so a burst of concurrent requests hitting the threshold at
// once still only ever blocks on the cheap append/swap, never on the
// database write itself.
type Buffer struct {
	mu      sync.Mutex
	records []Record
	pool    *pgxpool.Pool
}

// NewBuffer constructs an empty Buffer backed by pool.
func NewBuffer(pool *pgxpool.Pool) *Buffer {
	return &Buffer{pool: pool}
}

// Record buffers one request metric, normalizing path into its own
// path_pattern and extracting a request_firm_id if the path has that
// shape (see NormalizePathPattern/ExtractFirmID). Flushes inline
// (synchronously, but with the actual write outside the buffer's own
// lock - see Buffer's own doc comment) the instant the buffer reaches
// maxBufferSize, rather than waiting for the next flushInterval tick.
func (b *Buffer) Record(method, path string, statusCode, durationMs int) {
	rec := Record{
		Method:        method,
		PathPattern:   NormalizePathPattern(path),
		StatusCode:    statusCode,
		DurationMs:    durationMs,
		RequestFirmID: ExtractFirmID(path),
		OccurredAt:    time.Now().UTC(),
	}

	toFlush := b.appendAndMaybeSwap(rec)
	if toFlush != nil {
		b.write(context.Background(), toFlush)
	}
}

// appendAndMaybeSwap is Record's entire critical section: append, then
// - only once the threshold is reached - detach the accumulated slice
// and reset the buffer, returning the detached slice for the caller to
// write outside the lock. Returns nil when no flush is due yet.
func (b *Buffer) appendAndMaybeSwap(rec Record) []Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.records = append(b.records, rec)
	if len(b.records) < maxBufferSize {
		return nil
	}
	detached := b.records
	b.records = nil
	return detached
}

// detachAll swaps out the entire current buffer (whatever length it
// currently is) for a fresh empty one - FlushNow/RunFlushLoop's own
// primitive, used for both the periodic tick and the final
// shutdown flush.
func (b *Buffer) detachAll() []Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.records) == 0 {
		return nil
	}
	detached := b.records
	b.records = nil
	return detached
}

// FlushNow writes every currently-buffered record to request_metrics
// immediately, regardless of how many are buffered - RunFlushLoop's own
// periodic-tick and final-shutdown-flush primitive, also exported
// directly for a caller (e.g. a test, or an operator-triggered flush)
// that wants to force a flush without waiting on either threshold.
func (b *Buffer) FlushNow(ctx context.Context) {
	toFlush := b.detachAll()
	if toFlush != nil {
		b.write(ctx, toFlush)
	}
}

// write is the one place this package ever touches the database - a
// single pgx.Batch insert, same "one round trip for the whole slice"
// shape internal/analytics.IngestEvents already establishes for its own
// batch insert. Errors are logged, not returned/retried: by the time
// write runs, the records are already gone from the in-memory buffer
// (detached by appendAndMaybeSwap/detachAll before write is ever
// called) - metrics collection is a best-effort observability signal,
// not a durability-critical write path, so a transient database error
// here drops that one batch of metrics rather than blocking or
// re-buffering requests that have already completed.
func (b *Buffer) write(ctx context.Context, records []Record) {
	if b.pool == nil {
		return
	}
	batch := &pgx.Batch{}
	for _, r := range records {
		batch.Queue(`
			INSERT INTO request_metrics (method, path_pattern, status_code, duration_ms, request_firm_id, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, r.Method, r.PathPattern, r.StatusCode, r.DurationMs, r.RequestFirmID, r.OccurredAt)
	}
	results := b.pool.SendBatch(ctx, batch)
	defer results.Close()
	for range records {
		if _, err := results.Exec(); err != nil {
			slog.Error("apm: flush request metrics batch", "err", fmt.Errorf("insert request metric: %w", err))
			return
		}
	}
}

// RunFlushLoop ticks every flushInterval, flushing whatever is buffered,
// until ctx is cancelled - at which point it flushes one final time
// before returning, so a graceful shutdown (cmd/server/main.go cancels
// this context on SIGTERM/SIGINT, see that file's own comment) never
// silently drops the tail of a buffer that hadn't yet reached
// maxBufferSize or the next flushInterval tick.
func (b *Buffer) RunFlushLoop(ctx context.Context) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			b.FlushNow(context.Background())
			return
		case <-ticker.C:
			b.FlushNow(ctx)
		}
	}
}
