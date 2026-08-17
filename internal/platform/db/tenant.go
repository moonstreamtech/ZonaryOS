// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

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
// ciaudit:ignore-firmid-check: this is the low-level RLS session
// primitive itself, not a caller-facing operation - it is fn's job (every
// caller in this codebase) to call permission.IsMember/Has inside the
// closure it passes here; see docs/DEVELOPMENT.md's Authorization
// checklist.
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

// WithInviteTokenContext runs fn inside a transaction scoped to token via
// the `app.current_invite_token` session setting - the read-side RLS
// carve-out migrations/0007_firm_invites.up.sql adds to firm_invites,
// mirroring WithUserContext's own bootstrap reasoning: it lets a caller
// look up their own invite (by the unguessable token they were given, the
// caller's real credential here) before any firm_id is known at all,
// exactly the chicken-and-egg gap WithUserContext solves for firm
// discovery. Only firm_invites' policy consults this setting - every
// other tenant-scoped table's isolation is untouched. Write access to
// firm_invites is NOT widened the same way (see that migration's WITH
// CHECK clause): a caller inside this transaction can still only
// INSERT/UPDATE firm_invites once it has also set app.current_firm_id
// itself, the same manual two-step sequencing internal/wizard.CreateDefaultFirm
// uses once its own firmID becomes known mid-transaction - see
// internal/invite.Accept for exactly that sequencing on this table.
func WithInviteTokenContext(ctx context.Context, pool *pgxpool.Pool, token string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_invite_token', $1, true)", token); err != nil {
		return fmt.Errorf("set app.current_invite_token: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// WithTwoFirmContext runs fn inside a SINGLE transaction that can be
// re-scoped between different firms' RLS contexts via the scope function
// fn is given - the primitive a genuinely atomic cross-firm write (one
// commit/rollback covering inserts into more than one firm's tenant-
// scoped tables) needs, which WithFirmContext itself cannot express:
// WithFirmContext always calls pool.Begin(ctx) itself, so two
// WithFirmContext calls are always two separate transactions/commits,
// not one atomic unit (if the second failed after the first committed,
// the first's writes would not be rolled back).
//
// scope(firmID) issues the same `SET LOCAL app.current_firm_id`
// set_config call WithFirmContext's own withSessionContext does, just
// against the one already-open tx rather than a fresh one - calling it
// again with a different firmID immediately re-scopes every subsequent
// statement on that tx to the new firm's RLS policies. Only one firm's
// context is ever active at a time; nothing here grants any query
// visibility across firms simultaneously; Never-Violate Rule 3 (isolation
// enforced by Postgres itself) still holds exactly as it does for every
// other WithFirmContext caller - this is two sequential RLS scopes
// sharing one commit, not a bypass.
//
// First sanctioned use: internal/consolidation.PostIntercompanyTransfer
// (multi-company/consolidation batch) - see its own doc comment for why
// posting matching journal entries into two different firms' books needs
// to be atomic. There is no other precedent for a cross-firm-atomic write
// anywhere else in this codebase; new callers should have an equally
// deliberate reason before reaching for this instead of two separate
// WithFirmContext calls.
//
// ciaudit:ignore-firmid-check: this is the low-level RLS session
// primitive itself, not a caller-facing operation - same reasoning as
// WithFirmContext's own suppression above. It is fn's job to run its own
// authorization check(s) for each firm it scopes into.
func WithTwoFirmContext(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx, scope func(firmID uuid.UUID) error) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	scope := func(firmID uuid.UUID) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_firm_id', $1, true)", firmID.String()); err != nil {
			return fmt.Errorf("set app.current_firm_id: %w", err)
		}
		return nil
	}

	if err := fn(ctx, tx, scope); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
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
