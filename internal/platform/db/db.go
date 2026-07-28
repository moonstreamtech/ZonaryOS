// Package db provides the shared Postgres connection pool and the
// tenant-context helper every tenant-scoped query must go through.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates and validates a connection pool. The DSN's role determines
// what the returned pool can do: a superuser/owner role (used by the
// migrator) bypasses Row-Level Security entirely, while the application
// runtime must connect as the unprivileged zonaryos_app role created in
// migrations/0001_core_schema.up.sql for RLS to have any effect.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}
