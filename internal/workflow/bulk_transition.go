// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/webhook"
)

// ErrBulkTransitionEmpty means BulkExecuteTransition was called with no
// instance ids at all - nothing to do, and not a case worth silently
// no-opping (a caller sending an empty list almost certainly has a bug).
var ErrBulkTransitionEmpty = errors.New("bulk transition requires at least one instance id")

// ErrBulkTransitionMixedDefinitions means the given instance ids don't
// all belong to the same workflow definition - Part 4's own explicit
// requirement ("must all be the same definition"), checked up front
// before any instance is touched, so a mixed-definition request fails
// fast with a clear reason rather than partway through (which the
// shared-transaction design below would roll back anyway, but a
// dedicated error is a much clearer signal than whatever ErrNoSuchTransition
// or ErrPermissionDenied the first mismatched instance happened to hit).
var ErrBulkTransitionMixedDefinitions = errors.New("bulk transition instances must all belong to the same workflow definition")

// BulkTransitionResult is BulkExecuteTransition's own success shape -
// every instance id it was given (order preserved) plus the state key
// they all moved to (necessarily the same key for all of them, since
// they're required to share one definition and one action).
type BulkTransitionResult struct {
	InstanceIDs []uuid.UUID
	ToStateKey  string
}

// BulkExecuteTransition executes actionKey on every instance in
// instanceIDs at once - Part 4 of the multi-tenant isolation hardening +
// performance batch. Preconditions, checked before any instance is
// touched:
//   - instanceIDs is non-empty (ErrBulkTransitionEmpty)
//   - every instance belongs to the same workflow definition
//     (ErrBulkTransitionMixedDefinitions)
//
// Beyond that, each instance still needs to be in a state that actually
// has actionKey as an outgoing transition, and the caller still needs to
// hold that transition's own permission_key - both enforced by
// executeTransitionTx exactly as ExecuteTransition enforces them for a
// single instance (see that function's own doc comment); a violation on
// ANY instance surfaces as this call's own error.
//
// Atomicity: every instance's transition (state change, audit log entry,
// and any workflow-to-ledger/inventory/logistics/CRM/invoicing bridge
// effects) runs inside ONE shared database transaction
// (zdb.WithFirmContext), not one transaction per instance - so a failure
// on instance N rolls back every instance already transitioned in this
// same call (1..N-1), not just instance N itself. Either every instance
// in instanceIDs ends up transitioned, or none of them do; there is no
// partial-success outcome to report.
//
// Unlike ExecuteTransition, this does NOT run EvaluateRules per instance
// after commit - a bulk operation firing N separate rule-evaluation
// passes (each its own read of the just-committed state) is deferred
// scope (see this batch's own "no async job execution" boundary); a rule
// that reacts to this transition on a single-instance basis still fires
// normally the next time that instance is transitioned through
// ExecuteTransition.
func BulkExecuteTransition(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, instanceIDs []uuid.UUID, actionKey string, payload map[string]any) (BulkTransitionResult, error) {
	if len(instanceIDs) == 0 {
		return BulkTransitionResult{}, ErrBulkTransitionEmpty
	}

	var result BulkTransitionResult
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		// Checked here, before the definition-lookup query below touches
		// any workflow_instances row: WithFirmContext's RLS scoping alone
		// only confines that query to rows whose firm_id matches firmID,
		// which by itself doesn't prove the caller has any right to that
		// firmID at all (same reasoning CurrentState's own doc comment
		// gives) - without this check first, a non-member supplying a
		// real firmID could learn whether/how many of instanceIDs exist
		// and share a definition before ever being denied.
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrInstanceNotFound
		}

		definitionIDs, err := tx.Query(ctx, `
			SELECT DISTINCT workflow_definition_id FROM workflow_instances WHERE id = ANY($1)
		`, instanceIDs)
		if err != nil {
			return fmt.Errorf("look up instance definitions: %w", err)
		}
		var seen []uuid.UUID
		for definitionIDs.Next() {
			var id uuid.UUID
			if err := definitionIDs.Scan(&id); err != nil {
				definitionIDs.Close()
				return err
			}
			seen = append(seen, id)
		}
		if err := definitionIDs.Err(); err != nil {
			return err
		}
		definitionIDs.Close()
		if len(seen) != 1 {
			// 0 rows means at least one id doesn't exist (or isn't
			// visible under RLS) - the per-instance lookup inside
			// executeTransitionTx would report that instance as
			// ErrInstanceNotFound anyway, but len(seen) != 1 already
			// covers "not exactly one shared definition" for both the
			// zero and the mixed case without a second round trip.
			if len(seen) == 0 {
				return ErrInstanceNotFound
			}
			return ErrBulkTransitionMixedDefinitions
		}

		var toStateKey string
		for _, instanceID := range instanceIDs {
			_, stateKey, _, err := executeTransitionTx(ctx, tx, firmID, userID, instanceID, actionKey, payload)
			if err != nil {
				return fmt.Errorf("instance %s: %w", instanceID, err)
			}
			toStateKey = stateKey
		}

		result = BulkTransitionResult{InstanceIDs: instanceIDs, ToStateKey: toStateKey}
		return nil
	})
	if err != nil {
		return BulkTransitionResult{}, err
	}

	// One dispatch per instance, after the whole batch has committed -
	// same "webhook.Dispatch only ever runs after the operation it
	// describes has actually happened" reasoning ExecuteTransition's own
	// call site follows.
	for _, instanceID := range result.InstanceIDs {
		webhook.Dispatch(pool, firmID, webhook.EventWorkflowTransitionExecuted, map[string]any{
			"instanceId": instanceID.String(),
			"actionKey":  actionKey,
			"toState":    result.ToStateKey,
		})
	}
	return result, nil
}
