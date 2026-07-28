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
func WithFirmContext(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return withSessionContext(ctx, pool, "app.current_firm_id", firmID, fn)
}

// WithUserContext runs fn inside a transaction scoped to userID via the
// `app.current_user_id` session setting. It exists for exactly one purpose:
// letting a freshly-authenticated user discover which firm(s) they belong
// to (see migrations/0002_user_scoped_discovery.up.sql) before any firm
// context can be established. Every other tenant-scoped operation should
// use WithFirmContext, not this.
func WithUserContext(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return withSessionContext(ctx, pool, "app.current_user_id", userID, fn)
}

// withSessionContext sets the given Postgres session setting to id.String()
// for the lifetime of one transaction, then runs fn. set_config(..., true)
// (equivalent to `SET LOCAL`) is used instead of string-interpolating a SQL
// statement so the value is always passed as a bound parameter, with no SQL
// injection surface.
func withSessionContext(ctx context.Context, pool *pgxpool.Pool, setting string, id uuid.UUID, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT set_config($1, $2, true)", setting, id.String()); err != nil {
		return fmt.Errorf("set %s: %w", setting, err)
	}

	if err := fn(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
