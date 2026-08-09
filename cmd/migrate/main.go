// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Command migrate applies pending database migrations. It connects with
// the privileged (owner/superuser) role - see internal/platform/db.Migrate.
package main

import (
	"log/slog"
	"os"

	"github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

func init() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
}

func main() {
	dsn := os.Getenv("ZONARYOS_MIGRATE_DATABASE_URL")
	if dsn == "" {
		slog.Error("ZONARYOS_MIGRATE_DATABASE_URL must be set")
		os.Exit(1)
	}

	if err := db.Migrate(dsn); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}

	slog.Info("migrations applied")
}
