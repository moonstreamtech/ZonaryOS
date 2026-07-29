// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package auditlog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// PurgeOlderThan deletes every audit_log row across every firm with
// occurred_at before cutoff, returning how many rows were removed.
//
// CONFIGURABLE-PENDING-LEGAL-REVIEW: this package hardcodes no retention
// duration anywhere - docs/OPEN_POINTS.md item 33 leaves both the exact
// retention period and whether automatic deletion is even appropriate
// (given the open KVKK question over view/read logging, see ViewAction)
// unresolved. cutoff is entirely the caller's responsibility to compute;
// the only caller in this codebase is cmd/auditpurge, which refuses to run
// at all unless an operator has explicitly set ZONARYOS_AUDIT_RETENTION_DAYS
// - there is no default duration to fall back to, and nothing in cmd/server
// invokes this automatically.
//
// audit_log is RLS-scoped per firm (migrations/0003_workflow_engine.up.sql),
// so a single unscoped DELETE would see zero rows through the unprivileged
// zonaryos_app connection this function uses (fail-closed by design, same
// as everywhere else in this codebase) - this loops over every firm and
// deletes within each one's own WithFirmContext transaction, rather than
// introducing a privileged/bypass-RLS connection just for this one
// maintenance operation.
func PurgeOlderThan(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time) (int64, error) {
	rows, err := pool.Query(ctx, `SELECT id FROM firms`)
	if err != nil {
		return 0, fmt.Errorf("list firms: %w", err)
	}
	var firmIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		firmIDs = append(firmIDs, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	rows.Close()

	var total int64
	for _, firmID := range firmIDs {
		err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `DELETE FROM audit_log WHERE occurred_at < $1`, cutoff)
			if err != nil {
				return err
			}
			total += tag.RowsAffected()
			return nil
		})
		if err != nil {
			return total, fmt.Errorf("purge firm %s: %w", firmID, err)
		}
	}
	return total, nil
}
