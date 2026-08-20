// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apm

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/pghealth"
)

// DefaultSelfHealingPollInterval mirrors every other background
// scheduler in this codebase (internal/scheduledreports.DefaultSchedulerPollInterval,
// internal/asset.MaintenanceSchedulerPollInterval, ...) - frequent
// enough that a partition is always created well ahead of any request
// actually needing it (EnsureFuturePartitions is a cheap idempotent
// no-op on every tick that finds nothing missing, see that function's
// own doc comment), infrequent enough this isn't a busy-poll loop.
const DefaultSelfHealingPollInterval = 1 * time.Hour

// staleQueryThreshold is Part 4's own "if any query has been running
// for more than 5 minutes" requirement.
const staleQueryThreshold = 5 * time.Minute

// RunSelfHealingScheduler runs ProcessSelfHealing every pollInterval
// until ctx is cancelled - the same RunX/ProcessX split every other
// background scheduler in this codebase already uses.
func RunSelfHealingScheduler(ctx context.Context, pool *pgxpool.Pool, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ProcessSelfHealing(ctx, pool)
		}
	}
}

// ProcessSelfHealing is RunSelfHealingScheduler's testable core: Part
// 4's two automatic remediation actions, run back to back on the same
// tick since both are cheap, read-mostly checks against the same
// database.
//
//  1. Partition creation (EnsureFuturePartitions): genuinely automatic
//     remediation - a missing partition is created, not just reported.
//  2. Stale lock detection (internal/pghealth.StaleQueries): logged via
//     slog.Warn only, per Part 4's own explicit "monitoring-only, no
//     automatic killing" scope boundary - this function never
//     terminates a backend, however long a query has been running.
func ProcessSelfHealing(ctx context.Context, pool *pgxpool.Pool) {
	if err := EnsureFuturePartitions(ctx, pool); err != nil {
		slog.Error("apm self-healing: ensure future partitions", "err", err)
	}

	stale, err := pghealth.StaleQueries(ctx, pool, staleQueryThreshold)
	if err != nil {
		slog.Error("apm self-healing: check stale queries", "err", err)
		return
	}
	for _, sq := range stale {
		slog.Warn("apm self-healing: stale query detected",
			"pid", sq.PID, "runningSeconds", sq.RunningSeconds, "queryFingerprint", sq.QueryFingerprint)
	}
}
