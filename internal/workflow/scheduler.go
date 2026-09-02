// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Scheduled/recurring rule support (Open Points item 7's remaining
// "(2) Schedule-triggered rules" gap) - deliberately PARTIAL, per this
// batch's own brief:
//
//   - No cron expressions - Rule.ScheduleInterval is a plain Go
//     time.Duration string ("1h", "24h", ...), re-applied after every run
//     (see runScheduledRule below). There is no "every Monday at 9am"
//     concept here, only "every N since the last run".
//   - No timezone handling beyond UTC - scheduled_for/ran_at are plain
//     timestamptz values compared against time.Now(), same as every other
//     timestamp in this codebase; there is no per-firm timezone setting.
//   - No distributed locking/job queue - this is one goroutine inside the
//     single server process (Open Points item 34: single-instance
//     deployment), polling Postgres directly. Running two instances of
//     this binary against the same database would double-process due
//     runs; nothing here prevents that, because nothing in this
//     deployment model needs it to yet.
//   - No condition tree, notify-only actions - see rules.go's
//     validateRuleShape for where this is actually enforced. A schedule
//     trigger has no triggering workflow_instance to evaluate a "field"/
//     "field_compare" condition against, or to run an ActionTransition/
//     ActionSetField action on, so this batch simply doesn't offer them
//     for TriggerSchedule rules rather than inventing ad hoc semantics
//     for "which instance" a timer-fired action would even apply to.
//
// The polling loop itself has no per-firm privilege escalation: firms has
// no RLS at all (it IS the tenant boundary, not tenant-scoped data - see
// migrations/0001_core_schema.up.sql), so listAllFirmIDs is a plain,
// unscoped read of that one small table; every actual scheduled_rule_runs
// read/write still goes through the ordinary firm-scoped
// db.WithFirmContext, one firm at a time, the same RLS-enforced path
// every other write in this codebase uses.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// SchedulerPollInterval is the background scheduler's production poll
// cadence - "runs every minute" per this batch's own brief. Tests pass a
// much shorter interval directly to RunScheduler instead of using this
// constant, so a real end-to-end firing can be observed without waiting
// a full minute of real time.
const SchedulerPollInterval = 1 * time.Minute

// dueRunsPerFirmCap bounds how many due runs a single poll processes per
// firm, so one firm with a backlog can never starve every other firm's
// own due runs within the same tick - the next tick picks up wherever
// this one left off.
const dueRunsPerFirmCap = 50

// RunScheduler runs ProcessDueScheduledRuns every pollInterval until ctx
// is cancelled - the "lightweight background goroutine in the server, not
// a separate process, not a cron daemon" this batch's brief asks for.
// cmd/server/main.go starts this with SchedulerPollInterval; tests pass a
// short interval (e.g. 100ms) to observe a real firing without waiting on
// real-world minutes.
func RunScheduler(ctx context.Context, pool *pgxpool.Pool, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ProcessDueScheduledRuns(ctx, pool)
		}
	}
}

// ProcessDueScheduledRuns is RunScheduler's testable core: find every
// pending scheduled_rule_runs row whose scheduled_for has passed, across
// every firm, and run it. Exported so an integration test can call it
// directly against a Postgres-seeded due run without needing to wait for
// a real ticker tick at all.
func ProcessDueScheduledRuns(ctx context.Context, pool *pgxpool.Pool) {
	firmIDs, err := listAllFirmIDs(ctx, pool)
	if err != nil {
		slog.Error("scheduler: list firms", "err", err)
		return
	}
	for _, firmID := range firmIDs {
		processDueRunsForFirm(ctx, pool, firmID)
	}
}

// listAllFirmIDs reads every firms.id - see this file's own doc comment
// for why this plain, unscoped read is safe (firms carries no RLS at
// all).
func listAllFirmIDs(ctx context.Context, pool *pgxpool.Pool) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `SELECT id FROM firms`)
	if err != nil {
		return nil, fmt.Errorf("list firms: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan firm id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type dueScheduledRun struct {
	id     uuid.UUID
	ruleID uuid.UUID
}

// getFirmTimezone resolves the firm's configured timezone from the firms table,
// falling back to UTC if unset or invalid.
func getFirmTimezone(ctx context.Context, tx pgx.Tx, firmID uuid.UUID) *time.Location {
	var tz string
	err := tx.QueryRow(ctx, `SELECT timezone FROM firms WHERE id = $1`, firmID).Scan(&tz)
	if err != nil || tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// processDueRunsForFirm lists firmID's own due runs (one short, firm-
// scoped transaction), then runs each one - deliberately NOT inside that
// same listing transaction: runScheduledRule below opens its own
// WithFirmContext transactions per run, the same "list in one tx, act in
// separate ones" shape EvaluateRules itself already uses (rules.go).
//
// ciaudit:ignore-firmid-check: called only by ProcessDueScheduledRuns,
// with firmID drawn from listAllFirmIDs (an internal, unscoped read of
// the firms table itself - see this file's own doc comment) - never from
// an HTTP caller, so there is no caller-supplied firmID to validate
// membership against here.
func processDueRunsForFirm(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) {
	var due []dueScheduledRun
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, rule_id FROM scheduled_rule_runs
			WHERE firm_id = $1 AND status = 'pending' AND scheduled_for <= now()
			ORDER BY scheduled_for
			LIMIT $2
		`, firmID, dueRunsPerFirmCap)
		if err != nil {
			return fmt.Errorf("list due scheduled runs: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var d dueScheduledRun
			if err := rows.Scan(&d.id, &d.ruleID); err != nil {
				return fmt.Errorf("scan due scheduled run: %w", err)
			}
			due = append(due, d)
		}
		return rows.Err()
	})
	if err != nil {
		slog.Error("scheduler: list due runs", "firmId", firmID, "err", err)
		return
	}

	for _, d := range due {
		runScheduledRule(ctx, pool, firmID, d.id, d.ruleID)
	}
}

// getRuleByIDTx reads one workflow_rules row by (firmID, ruleID) alone -
// unlike getRuleTx (rules.go), not further scoped by definitionKey, since
// the scheduler only ever has ruleID in hand.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// runScheduledRule, itself only reachable from the background scheduler
// (see processDueRunsForFirm's own comment for why firmID here is never
// caller-supplied).
func getRuleByIDTx(ctx context.Context, tx pgx.Tx, firmID, ruleID uuid.UUID) (Rule, error) {
	row := tx.QueryRow(ctx, `SELECT `+ruleColumns+` FROM workflow_rules WHERE id = $1 AND firm_id = $2`, ruleID, firmID)
	rule, err := scanRule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, ErrRuleNotFound
	}
	if err != nil {
		return Rule{}, fmt.Errorf("load workflow rule: %w", err)
	}
	return rule, nil
}

// runScheduledRule executes one due scheduled_rule_runs row: loads its
// rule, runs every (validation-enforced notify-only) action, marks the
// run 'ran', and - only if the rule still has actions and a
// ScheduleInterval - inserts the next run, all in one transaction. Any
// error (rule deleted/disabled, a malformed interval, ...) instead marks
// the run 'failed' with the error text, in its own separate transaction
// (the first one having rolled back), and does NOT reschedule - a failed
// or disabled rule doesn't keep silently re-queuing itself forever.
//
// ciaudit:ignore-firmid-check: called only by processDueRunsForFirm, with
// firmID drawn from listAllFirmIDs, never an HTTP caller - see that
// function's own comment.
func runScheduledRule(ctx context.Context, pool *pgxpool.Pool, firmID, runID, ruleID uuid.UUID) {
	runErr := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		rule, err := getRuleByIDTx(ctx, tx, firmID, ruleID)
		if err != nil {
			return err
		}
		if !rule.Enabled {
			return fmt.Errorf("rule %q is disabled", rule.Name)
		}
		if rule.Trigger != TriggerSchedule {
			return fmt.Errorf("rule %q is no longer schedule-triggered", rule.Name)
		}

		for _, action := range rule.Actions {
			// validateRuleShape already guarantees every action on a
			// schedule-triggered rule is ActionNotify - see that
			// function's own doc comment for why (no instance/payload
			// context exists for a timer to evaluate ActionTransition/
			// ActionSetField against).
			message := renderMessageTemplate(action.MessageTemplate, map[string]any{})
			slog.Info("scheduled rule fired", "firmId", firmID, "ruleId", rule.ID, "ruleName", rule.Name, "message", message)
		}

		if _, err := tx.Exec(ctx, `UPDATE scheduled_rule_runs SET status = 'ran', ran_at = now() WHERE id = $1`, runID); err != nil {
			return fmt.Errorf("mark scheduled run ran: %w", err)
		}

		// "if it has actions, schedule the next run" - a rule with zero
		// actions today (matched but does nothing) is a one-shot; there
		// is nothing meaningful to keep re-running.
		if len(rule.Actions) > 0 && rule.ScheduleInterval != nil {
			interval, parseErr := time.ParseDuration(*rule.ScheduleInterval)
			if parseErr != nil {
				return fmt.Errorf("parse schedule interval %q: %w", *rule.ScheduleInterval, parseErr)
			}
			loc := getFirmTimezone(ctx, tx, firmID)
			scheduledFor := time.Now().In(loc).Add(interval)
			if _, err := tx.Exec(ctx, `
				INSERT INTO scheduled_rule_runs (firm_id, rule_id, scheduled_for)
				VALUES ($1, $2, $3)
			`, firmID, rule.ID, scheduledFor); err != nil {
				return fmt.Errorf("schedule next run: %w", err)
			}
		}
		return nil
	})
	if runErr == nil {
		return
	}

	slog.Warn("scheduled rule run failed", "firmId", firmID, "runId", runID, "ruleId", ruleID, "err", runErr)
	_ = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE scheduled_rule_runs SET status = 'failed', ran_at = now(), error_text = $1 WHERE id = $2
		`, runErr.Error(), runID)
		return err
	})
}
