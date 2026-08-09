// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Command auditpurge deletes audit_log rows older than an operator-supplied
// retention window (ZONARYOS_AUDIT_RETENTION_DAYS). It is a manually-invoked
// maintenance command, not something cmd/server ever runs automatically:
// the audit trail's actual retention period - and whether automatic
// deletion is even appropriate given the open KVKK question over view/read
// logging - is not yet decided (see docs/OPEN_POINTS.md item 33). This
// command hardcodes no default duration (in particular, not the Vision
// doc's own unconfirmed "at least 10 years" figure) and refuses to run at
// all unless ZONARYOS_AUDIT_RETENTION_DAYS is explicitly set to a positive
// integer.
package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

func init() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
}

func main() {
	dsn := os.Getenv("ZONARYOS_DATABASE_URL")
	if dsn == "" {
		slog.Error("ZONARYOS_DATABASE_URL must be set")
		os.Exit(1)
	}

	retentionDays, err := strconv.Atoi(os.Getenv("ZONARYOS_AUDIT_RETENTION_DAYS"))
	if err != nil || retentionDays <= 0 {
		slog.Error("ZONARYOS_AUDIT_RETENTION_DAYS must be set to a positive integer - " +
			"the audit log retention period is not yet finalized (docs/OPEN_POINTS.md item 33); " +
			"refusing to guess a duration and delete data")
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		slog.Error("open database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	deleted, err := auditlog.PurgeOlderThan(ctx, pool, cutoff)
	if err != nil {
		slog.Error("purge audit log", "err", err)
		os.Exit(1)
	}
	slog.Info("purged audit log rows", "deleted", deleted, "cutoff", cutoff.Format(time.RFC3339)) // #nosec G706 -- deleted/cutoff are a DB row count and a computed timestamp, not attacker-controlled input; this is an operator-invoked CLI, not a network-facing log sink
}
