package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithFirmContext runs fn inside a transaction scoped to firmID via the
// `app.current_firm_id` Postgres session setting that every tenant-scoped
// table's Row-Level Security policy checks (see
// migrations/0001_core_schema.up.sql). This is the only sanctioned way for
// application code to touch a tenant-scoped table: isolation comes from the
// database engine enforcing the policy against this setting, never from an
// application-level "WHERE firm_id = ?" that could be forgotten in some
// code path (Never-Violate Rule 3).
//
// set_config(..., true) is used instead of string-interpolating a `SET
// LOCAL` statement so the firm ID is always passed as a bound parameter,
// with no SQL injection surface.
func WithFirmContext(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_firm_id', $1, true)", firmID.String()); err != nil {
		return fmt.Errorf("set firm context: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
