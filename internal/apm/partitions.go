// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apm

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/pghealth"
)

// monthsAheadToMaintain is Part 4's own "checks monthly if new
// request_metrics partitions are needed (2 months ahead)" requirement -
// this includes the current month itself (i=0), so "2 months ahead"
// means 3 real partitions maintained at any time: this month, next
// month, and the one after.
const monthsAheadToMaintain = 3

// PartitionName is request_metrics' own partition-naming convention
// (migrations/0041_performance_monitoring.up.sql's own doc comment):
// request_metrics_YYYY_MM, zero-padded, derived purely from the month's
// own first-of-month timestamp - the same name both the initial
// migration's hardcoded partitions and this function's own
// runtime-created ones use, so a partition this function creates is
// indistinguishable from one the migration created.
func PartitionName(month time.Time) string {
	return fmt.Sprintf("request_metrics_%04d_%02d", month.Year(), month.Month())
}

// EnsureFuturePartitions creates any missing request_metrics partition
// for the current month through monthsAheadToMaintain-1 months ahead,
// via CREATE TABLE IF NOT EXISTS ... PARTITION OF ... FOR VALUES FROM
// ... TO ... - idempotent by construction (IF NOT EXISTS), so calling
// this on every scheduler tick (see RunSelfHealingScheduler) rather than
// only once a month is simply a cheap no-op on every tick that finds
// nothing missing.
func EnsureFuturePartitions(ctx context.Context, pool *pgxpool.Pool) error {
	now := time.Now().UTC()
	for i := 0; i < monthsAheadToMaintain; i++ {
		monthStart := time.Date(now.Year(), now.Month()+time.Month(i), 1, 0, 0, 0, 0, time.UTC)
		monthEnd := monthStart.AddDate(0, 1, 0)
		name := PartitionName(monthStart)

		// #nosec G201 -- name/the two date bounds are never caller-
		// supplied: name comes from PartitionName (a fixed
		// "request_metrics_YYYY_MM" format derived only from
		// time.Now()), and monthStart/monthEnd are formatted from that
		// same computed time.Time - there is no code path from an HTTP
		// request or any other external input into this string. Postgres
		// DDL (CREATE TABLE ... PARTITION OF) has no parameterized-
		// identifier form the way a DML WHERE clause does, so a
		// server-side-only computed name is the only way to express this
		// at all.
		if _, err := pool.Exec(ctx, fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s PARTITION OF request_metrics
			FOR VALUES FROM ('%s') TO ('%s')
		`, name, monthStart.Format("2006-01-02"), monthEnd.Format("2006-01-02"))); err != nil {
			return fmt.Errorf("ensure partition %q: %w", name, err)
		}
	}
	return nil
}

// CheckPartitionHealth reports which of the next monthsAheadToMaintain
// months (current month first) are still missing their own real
// partition - the read-only counterpart EnsurePartitionHealth's own
// GET /health?detail=true "partition health" field uses, built on
// internal/pghealth's shared to_regclass check rather than a second
// query.
func CheckPartitionHealth(ctx context.Context, pool *pgxpool.Pool) (pghealth.PartitionHealth, error) {
	return pghealth.CheckPartitionHealth(ctx, pool, PartitionName, monthsAheadToMaintain)
}
