// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ratelimit

import (
	"context"
	"log/slog"
	"time"
)

// logFlushInterval is Part 2's own "log rate limit hits via slog
// (aggregate, not per-request logging)" requirement - a per-request
// slog.Warn on every single 429 would itself become a denial-of-service
// vector against this server's own logging (a caller flooding a
// low-ceiling category, e.g. the 2 req/min export limit, would generate
// a proportional flood of log lines), so hits are counted in memory
// (Limiter.recordRejection) and flushed as one aggregate line per
// (firm, category) key every logFlushInterval instead.
const logFlushInterval = 1 * time.Minute

// RunLogFlushLoop logs one aggregate slog.Warn line per (firm, category)
// key that had at least one rejection since the last flush, every
// logFlushInterval, until ctx is cancelled - the same RunX shape every
// other background scheduler in this codebase already uses, though this
// one has no corresponding "ProcessX" split: there is no meaningful unit
// test for "did slog.Warn get called" beyond calling
// snapshotAndResetRejections directly (see limiter_test.go), so the
// logging side is deliberately kept this simple rather than manufacturing
// a ProcessX layer with nothing else to call it.
func RunLogFlushLoop(ctx context.Context, limiter *Limiter) {
	ticker := time.NewTicker(logFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			logRejections(limiter)
		}
	}
}

func logRejections(limiter *Limiter) {
	snapshot := limiter.snapshotAndResetRejections()
	for bucketKey, count := range snapshot {
		slog.Warn("ratelimit: requests rejected in the last interval", "bucketKey", bucketKey, "count", count, "intervalSeconds", int(logFlushInterval.Seconds()))
	}
}
