// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package onboarding tracks each firm member's first-run onboarding
// checklist: which of the fixed set of Steps they've completed, and
// whether they've dismissed the checklist widget entirely. Steps are
// product decisions, not tenant configuration - defined as Go constants
// below, not a database-configurable list (see the design brief's own
// "define as constants, not DB config" instruction).
//
// CompleteStep is this package's own equivalent of internal/webhook.Dispatch:
// a fire-and-forget call other packages' own POST endpoints make after
// their own transaction commits, so a firm's onboarding progress updates
// itself automatically as the user takes the very actions each step
// describes - no separate "mark complete" endpoint, and no hard
// dependency from e.g. internal/inventory on this package's own success
// (a failure to record onboarding progress must never fail the caller's
// own request). See this package's own doc comment on CompleteStep for
// why it's called this way rather than inline inside each entity
// function's own transaction.
package onboarding

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// Step is one of the fixed onboarding checklist items - a product
// decision (this design brief's own numbered list), not something a firm
// or plugin can add to or reorder.
type Step string

const (
	StepCreateFirstWorkflow Step = "create_first_workflow"
	StepAddFirstProduct     Step = "add_first_product"
	StepInviteTeamMember    Step = "invite_team_member"
	StepRunFirstReport      Step = "run_first_report"
	StepConfigureWebhook    Step = "configure_webhook"
)

// Steps is the fixed, ordered checklist - the exact order the dashboard
// widget renders them in.
var Steps = []Step{
	StepCreateFirstWorkflow,
	StepAddFirstProduct,
	StepInviteTeamMember,
	StepRunFirstReport,
	StepConfigureWebhook,
}

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm.
	ErrFirmNotFound = errors.New("firm not found")
)

// Progress is one user_onboarding_progress row.
type Progress struct {
	FirmID         uuid.UUID
	UserID         uuid.UUID
	CompletedSteps []Step
	DismissedAt    *string // RFC3339, nil when not dismissed
}

// GetProgress returns userID's onboarding progress within firmID,
// creating an empty row on first read if none exists yet - a user who
// has never triggered any step and never dismissed the widget still gets
// a well-formed Progress{} rather than a "not found" error, since "no
// progress yet" is the normal starting state, not an error condition.
// Member-gated: reading your own onboarding checklist is ordinary access,
// the same tier as internal/inventory.ListProducts.
func GetProgress(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) (Progress, error) {
	var progress Progress
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		p, err := getOrCreateProgressTx(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		progress = p
		return nil
	})
	if err != nil {
		return Progress{}, err
	}
	return progress, nil
}

// getOrCreateProgressTx is an internal helper, called only by
// GetProgress/Dismiss/CompleteStep after each of their own
// permission.IsMember checks has already run in the same transaction -
// firmID here scopes the query (defense in depth alongside RLS), it is
// not itself an authorization decision.
//
// ciaudit:ignore-firmid-check: internal helper, see doc comment above.
func getOrCreateProgressTx(ctx context.Context, tx pgx.Tx, firmID, userID uuid.UUID) (Progress, error) {
	var (
		completedSteps []string
		dismissedAt    *string
	)
	err := tx.QueryRow(ctx, `
		SELECT completed_steps, dismissed_at::text
		FROM user_onboarding_progress WHERE firm_id = $1 AND user_id = $2
	`, firmID, userID).Scan(&completedSteps, &dismissedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_onboarding_progress (firm_id, user_id) VALUES ($1, $2)
			ON CONFLICT (firm_id, user_id) DO NOTHING
		`, firmID, userID); err != nil {
			return Progress{}, fmt.Errorf("insert onboarding progress: %w", err)
		}
		return Progress{FirmID: firmID, UserID: userID, CompletedSteps: []Step{}}, nil
	}
	if err != nil {
		return Progress{}, fmt.Errorf("look up onboarding progress: %w", err)
	}

	steps := make([]Step, 0, len(completedSteps))
	for _, s := range completedSteps {
		steps = append(steps, Step(s))
	}
	return Progress{FirmID: firmID, UserID: userID, CompletedSteps: steps, DismissedAt: dismissedAt}, nil
}

// Dismiss marks the onboarding checklist dismissed for userID within
// firmID - once set, the dashboard widget never shows again for that
// user/firm pair, regardless of how many steps remain incomplete.
// Member-gated: dismissing your own checklist is the same tier as
// reading it (GetProgress).
func Dismiss(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) (Progress, error) {
	var progress Progress
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		if _, err := getOrCreateProgressTx(ctx, tx, firmID, userID); err != nil {
			return err
		}

		var (
			completedSteps []string
			dismissedAt    *string
		)
		if err := tx.QueryRow(ctx, `
			UPDATE user_onboarding_progress SET dismissed_at = now()
			WHERE firm_id = $1 AND user_id = $2
			RETURNING completed_steps, dismissed_at::text
		`, firmID, userID).Scan(&completedSteps, &dismissedAt); err != nil {
			return fmt.Errorf("dismiss onboarding progress: %w", err)
		}

		steps := make([]Step, 0, len(completedSteps))
		for _, s := range completedSteps {
			steps = append(steps, Step(s))
		}
		progress = Progress{FirmID: firmID, UserID: userID, CompletedSteps: steps, DismissedAt: dismissedAt}
		return nil
	})
	if err != nil {
		return Progress{}, err
	}
	return progress, nil
}

// CompleteStep records that userID has completed step within firmID -
// idempotent (completing an already-completed step is a no-op, backed by
// array-append-if-absent rather than a UNIQUE constraint/upsert dance).
// Called fire-and-forget, in its own goroutine, by the POST endpoint
// whose action the step represents (internal/workflow.DefineWorkflowForFirm,
// internal/inventory.CreateProduct, internal/invite.Generate,
// internal/reports.RunReport, internal/webhook.CreateWebhook) - same
// "never let a side-effect's failure fail the caller's own request"
// convention internal/webhook.Dispatch already establishes for webhook
// delivery. A failure here is logged, never returned to the caller: an
// onboarding checklist tick is a UX nicety, not a business-critical write
// worth coupling five unrelated packages' own success to.
//
// ciaudit:ignore-firmid-check: fire-and-forget side-effect helper, called
// only after the caller's own permission.IsMember/IsOwner check has
// already run and committed successfully; firmID here scopes the write
// (defense in depth alongside RLS), it is not itself an authorization
// decision.
func CompleteStep(pool *pgxpool.Pool, firmID, userID uuid.UUID, step Step) {
	go func() {
		ctx := context.Background()
		err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := getOrCreateProgressTx(ctx, tx, firmID, userID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `
				UPDATE user_onboarding_progress
				SET completed_steps = array_append(completed_steps, $3)
				WHERE firm_id = $1 AND user_id = $2 AND NOT ($3 = ANY(completed_steps))
			`, firmID, userID, string(step))
			return err
		})
		if err != nil {
			slog.Warn("onboarding: record step completion failed", "firmId", firmID, "userId", userID, "step", step, "err", err)
		}
	}()
}
