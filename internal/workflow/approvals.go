// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Approval workflow (Open Points item 41): an approval is a
// pending_approvals row plus a notification (internal/notification) to
// every firm member who holds the proposed action's own permission key -
// what a non-autonomous rule match (EvaluateRules, rules.go) creates
// instead of only writing a "pending approval" audit_log entry with
// nowhere to actually act on it. Approve executes the proposed transition
// for real (through ExecuteTransition, so the same permission check, RLS
// scoping, and effects that any normal transition gets); Reject marks the
// approval resolved and notifies whoever originally requested it.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/notification"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/webhook"
)

// DefaultApprovalTTL is how long a pending approval stays resolvable
// before it lazily transitions to 'expired' (checkResolvable/markExpired) - a
// provisional default, same "no product decision has actually set this
// yet" posture internal/license.DefaultGracePeriod's own doc comment
// documents for its own provisional constant.
const DefaultApprovalTTL = 7 * 24 * time.Hour

// ApprovalStatus is pending_approvals.status's closed vocabulary - a real
// DB CHECK constraint (migrations/0019_notifications_approvals_scheduling.up.sql).
type ApprovalStatus string

const (
	ApprovalStatusPending  ApprovalStatus = "pending"
	ApprovalStatusApproved ApprovalStatus = "approved"
	ApprovalStatusRejected ApprovalStatus = "rejected"
	ApprovalStatusExpired  ApprovalStatus = "expired"
)

// PendingApproval is one pending_approvals row.
type PendingApproval struct {
	ID                uuid.UUID
	FirmID            uuid.UUID
	InstanceID        uuid.UUID
	ProposedActionKey string
	// PermissionKey is recorded once at creation time, not re-derived from
	// the instance's current state - see the migration's own doc comment
	// for why. Gates who can see/attempt this approval; Approve itself
	// still re-validates for real via ExecuteTransition's own permission
	// check.
	PermissionKey string
	RequestedBy   uuid.UUID
	RuleID        *uuid.UUID
	Status        ApprovalStatus
	ResolvedBy    *uuid.UUID
	ResolvedAt    *time.Time
	ExpiresAt     *time.Time
	CreatedAt     time.Time
}

const (
	notificationTypeApprovalPending  = "approval_pending"
	notificationTypeApprovalRejected = "approval_rejected"
)

const approvalColumns = "id, firm_id, instance_id, proposed_action_key, permission_key, requested_by, rule_id, status, resolved_by, resolved_at, expires_at, created_at"

func scanPendingApproval(row pgx.Row) (PendingApproval, error) {
	var a PendingApproval
	var status string
	if err := row.Scan(&a.ID, &a.FirmID, &a.InstanceID, &a.ProposedActionKey, &a.PermissionKey, &a.RequestedBy, &a.RuleID,
		&status, &a.ResolvedBy, &a.ResolvedAt, &a.ExpiresAt, &a.CreatedAt); err != nil {
		return PendingApproval{}, err
	}
	a.Status = ApprovalStatus(status)
	return a, nil
}

// firstTransitionAction returns the first ActionTransition entry in
// actions, if any. A non-autonomous rule's approval flow only ever
// concerns itself with this one action: a "notify"/"set_field" action has
// no permission_key of its own to gate an approval on, and this batch
// deliberately tracks at most one proposed transition per rule match
// (documented scope boundary, not a hidden limitation) - a rule with
// multiple transition actions only gets an approval for the first one.
func firstTransitionAction(actions []Action) (Action, bool) {
	for _, a := range actions {
		if a.Type == ActionTransition {
			return a, true
		}
	}
	return Action{}, false
}

// resolvePermissionKeyTx looks up the permission_key a transition from
// stateKey via actionKey requires, within definitionKey/firmID - the same
// lookup ExecuteTransition itself performs (engine.go), factored out here
// so EvaluateRules' non-autonomous branch can resolve it before the
// instance necessarily still has that exact from-state (state can move
// between rule-match time and eventual approval).
//
// ciaudit:ignore-firmid-check: internal helper called only from
// createPendingApprovalAndNotify, itself only reachable from
// EvaluateRules' already-authorized flow (rules.go) - firmID here scopes
// a read query, not a fresh authorization decision.
func resolvePermissionKeyTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, definitionKey, stateKey, actionKey string) (string, error) {
	var permissionKey string
	err := tx.QueryRow(ctx, `
		SELECT wt.permission_key
		FROM workflow_transitions wt
		JOIN workflow_definitions wd ON wd.id = wt.workflow_definition_id
		JOIN workflow_states ws ON ws.id = wt.from_state_id
		WHERE wd.firm_id = $1 AND wd.key = $2 AND ws.key = $3 AND wt.action_key = $4
	`, firmID, definitionKey, stateKey, actionKey).Scan(&permissionKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoSuchTransition
	}
	if err != nil {
		return "", fmt.Errorf("resolve transition permission key: %w", err)
	}
	return permissionKey, nil
}

// listUsersWithPermissionTx returns every user in firmID who holds
// permissionKey through any role - the fan-out list
// createPendingApprovalAndNotify notifies.
//
// ciaudit:ignore-firmid-check: internal helper called only from
// createPendingApprovalAndNotify/ListPendingApprovals's own already-
// authorized flow - firmID here scopes a read query, not a fresh
// authorization decision.
func listUsersWithPermissionTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, permissionKey string) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT ufr.user_id
		FROM user_firm_roles ufr
		JOIN role_permissions rp ON rp.role_id = ufr.role_id AND rp.firm_id = ufr.firm_id
		WHERE ufr.firm_id = $1 AND rp.permission_key = $2
	`, firmID, permissionKey)
	if err != nil {
		return nil, fmt.Errorf("list users with permission: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan user id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// createPendingApprovalAndNotify is EvaluateRules' non-autonomous-match
// entry point (rules.go): resolves the proposed transition's
// permission_key, inserts the pending_approvals row, and notifies every
// firm member who holds that permission - all in one transaction, so a
// partial failure (e.g. the notify fan-out) never leaves an approval row
// with nobody told about it.
//
// ciaudit:ignore-firmid-check: called only from EvaluateRules' own
// already-authorized flow (rules.go) - firmID here scopes which firm's
// approval/notifications get written, it is not itself a fresh
// authorization decision.
func createPendingApprovalAndNotify(ctx context.Context, pool *pgxpool.Pool, firmID, requestedBy, instanceID uuid.UUID, definitionKey, stateKey string, rule Rule, actionKey string) (PendingApproval, error) {
	var approval PendingApproval
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		permissionKey, err := resolvePermissionKeyTx(ctx, tx, firmID, definitionKey, stateKey, actionKey)
		if err != nil {
			return err
		}

		expiresAt := time.Now().Add(DefaultApprovalTTL)
		var ruleID *uuid.UUID
		if rule.ID != uuid.Nil {
			id := rule.ID
			ruleID = &id
		}

		approval = PendingApproval{
			FirmID: firmID, InstanceID: instanceID, ProposedActionKey: actionKey, PermissionKey: permissionKey,
			RequestedBy: requestedBy, RuleID: ruleID, Status: ApprovalStatusPending, ExpiresAt: &expiresAt,
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO pending_approvals (firm_id, instance_id, proposed_action_key, permission_key, requested_by, rule_id, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id, created_at
		`, firmID, instanceID, actionKey, permissionKey, requestedBy, ruleID, expiresAt).Scan(&approval.ID, &approval.CreatedAt); err != nil {
			return fmt.Errorf("insert pending approval: %w", err)
		}

		recipients, err := listUsersWithPermissionTx(ctx, tx, firmID, permissionKey)
		if err != nil {
			return err
		}

		payload := map[string]any{
			"instanceId":        instanceID.String(),
			"proposedActionKey": actionKey,
			"pendingApprovalId": approval.ID.String(),
			"ruleId":            rule.ID.String(),
			"ruleName":          rule.Name,
			"definitionKey":     definitionKey,
		}
		title := fmt.Sprintf("Approval needed: %s", rule.Name)
		body := fmt.Sprintf("Rule %q proposes running action %q on a workflow instance and needs your approval.", rule.Name, actionKey)
		return notification.CreateForRecipientsTx(ctx, tx, firmID, recipients, notificationTypeApprovalPending, title, body, payload)
	})
	if err != nil {
		return PendingApproval{}, err
	}

	webhook.Dispatch(pool, firmID, webhook.EventApprovalPending, map[string]any{
		"pendingApprovalId": approval.ID.String(),
		"instanceId":        instanceID.String(),
		"proposedActionKey": actionKey,
		"ruleName":          rule.Name,
	})

	return approval, nil
}

// RequestApproval creates a pending_approvals row (and notifies every firm
// member holding the relevant permission) for instanceID's actionKey
// transition, OUTSIDE of the rule-engine's own EvaluateRules/non-
// autonomous-rule-match path - for callers (like internal/absence) that
// need a deterministic, always-required approval on a specific action
// rather than a firm-configured rule. Uses the same pending_approvals +
// notification mechanism EvaluateRules itself uses
// (createPendingApprovalAndNotify), with an empty Rule{} (RuleID stays
// NULL - "no rule triggered this, application logic did"). Rule{}'s zero
// value is handled correctly by createPendingApprovalAndNotify already
// (its own `if rule.ID != uuid.Nil` guard leaves ruleID nil), and an empty
// Rule.Name just produces a slightly generic "Approval needed: " title/
// body with no rule name - acceptable, not worth a special case.
//
// ciaudit:ignore-firmid-check: thin wrapper around
// createPendingApprovalAndNotify, which itself makes no authorization
// decision (same as EvaluateRules' own call site) - the caller (e.g.
// internal/absence.CreateAbsence) is responsible for having already
// checked the requester's standing (permission.IsMember) before calling
// this, the same division of responsibility
// accounting.PostJournalEntryTx's own doc comment documents for itself.
func RequestApproval(ctx context.Context, pool *pgxpool.Pool, firmID, requestedBy, instanceID uuid.UUID, definitionKey, stateKey, actionKey string) (PendingApproval, error) {
	return createPendingApprovalAndNotify(ctx, pool, firmID, requestedBy, instanceID, definitionKey, stateKey, Rule{}, actionKey)
}

// ListPendingApprovals returns every still-pending approval in firmID
// that userID could actually act on (holds the approval's own
// permission_key) - member-gated first, then filtered by permission, not
// by requested_by: an approval is meant to be seen by whoever CAN resolve
// it, not only whoever originally triggered it.
func ListPendingApprovals(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]PendingApproval, error) {
	var approvals []PendingApproval
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrApprovalNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT `+approvalColumns+`
			FROM pending_approvals pa
			WHERE pa.firm_id = $1 AND pa.status = 'pending'
			  AND EXISTS (
				SELECT 1 FROM user_firm_roles ufr
				JOIN role_permissions rp ON rp.role_id = ufr.role_id AND rp.firm_id = ufr.firm_id
				WHERE ufr.firm_id = $1 AND ufr.user_id = $2 AND rp.permission_key = pa.permission_key
			  )
			ORDER BY pa.created_at DESC
		`, firmID, userID)
		if err != nil {
			return fmt.Errorf("list pending approvals: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanPendingApproval(rows)
			if err != nil {
				return fmt.Errorf("scan pending approval: %w", err)
			}
			approvals = append(approvals, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return approvals, nil
}

// loadApprovalTx reads one pending_approvals row scoped to (approvalID,
// firmID).
//
// ciaudit:ignore-firmid-check: internal helper called only by Approve/
// Reject, both of which have already run permission.IsMember before
// reaching this call - firmID here scopes a read query, not a fresh
// authorization decision.
func loadApprovalTx(ctx context.Context, tx pgx.Tx, firmID, approvalID uuid.UUID) (PendingApproval, error) {
	row := tx.QueryRow(ctx, `SELECT `+approvalColumns+` FROM pending_approvals WHERE id = $1 AND firm_id = $2`, approvalID, firmID)
	a, err := scanPendingApproval(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PendingApproval{}, ErrApprovalNotFound
	}
	if err != nil {
		return PendingApproval{}, fmt.Errorf("load pending approval: %w", err)
	}
	return a, nil
}

// checkResolvable enforces "expired approvals cannot be resolved":
// already-resolved -> ErrApprovalNotPending; past its own expires_at ->
// ErrApprovalExpired. Deliberately read-only (no write here, even though
// the expired case IS eventually a status='expired' write - see
// markExpired's own doc comment for why that write can't happen inside
// the same transaction this check runs in) - a still-pending, non-expired
// approval passes through unchanged.
func checkResolvable(approval PendingApproval) error {
	if approval.Status != ApprovalStatusPending {
		return ErrApprovalNotPending
	}
	if approval.ExpiresAt != nil && approval.ExpiresAt.Before(time.Now()) {
		return ErrApprovalExpired
	}
	return nil
}

// markExpired lazily flips approvalID's status to 'expired', in its own
// committed transaction - deliberately NOT inside the same transaction
// checkResolvable's caller is already in: that transaction is about to
// return a non-nil error (ErrApprovalExpired) to signal "not resolvable",
// and db.WithFirmContext rolls the whole transaction back whenever its
// callback returns an error (see that function's own doc comment) - a
// write made just before returning that same error would be silently
// undone along with it. Best-effort: a failure to persist the expiry
// flag isn't itself the caller's problem to handle - the caller already
// has the real ErrApprovalExpired to return, and the next Approve/Reject
// attempt (or ListPendingApprovals read) will simply re-evaluate the same
// still-unwritten expiry from expires_at again.
//
// ciaudit:ignore-firmid-check: called only by Approve/Reject, both of
// which have already run permission.IsMember/Has before reaching the
// expired branch that calls this - firmID here scopes the write, not a
// fresh authorization decision.
func markExpired(ctx context.Context, pool *pgxpool.Pool, firmID, approvalID uuid.UUID) {
	_ = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE pending_approvals SET status = 'expired' WHERE id = $1 AND status = 'pending'`, approvalID)
		return err
	})
}

// Approve executes approvalID's proposed transition for real, through
// ExecuteTransition - the same permission check (against the transition's
// LIVE permission_key, not just the one recorded on the approval at
// request time), RLS scoping, and effects any ordinary transition gets.
//
// Two phases, deliberately not one transaction: the first atomically
// claims the approval (status pending -> approved, only if it's still
// 'pending' - the WHERE status = 'pending' guards against two concurrent
// Approve calls both succeeding), so ExecuteTransition itself runs
// outside that claim's own transaction (it manages its own). If
// ExecuteTransition then fails (e.g. the instance moved to a state with
// no such transition since the approval was requested), the claim is
// reverted in a compensating update, leaving the approval 'pending' and
// retryable rather than permanently stuck in a false "approved" state
// with no actual side effect.
func Approve(ctx context.Context, pool *pgxpool.Pool, firmID, userID, approvalID uuid.UUID) (PendingApproval, error) {
	var approval PendingApproval
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrApprovalNotFound
		}

		a, err := loadApprovalTx(ctx, tx, firmID, approvalID)
		if err != nil {
			return err
		}
		allowed, err := permission.Has(ctx, tx, firmID, userID, a.PermissionKey)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrPermissionDenied
		}
		if err := checkResolvable(a); err != nil {
			return err
		}

		row := tx.QueryRow(ctx, `
			UPDATE pending_approvals SET status = 'approved', resolved_by = $1, resolved_at = now()
			WHERE id = $2 AND status = 'pending'
			RETURNING `+approvalColumns, userID, approvalID)
		claimed, err := scanPendingApproval(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrApprovalNotPending
		}
		if err != nil {
			return fmt.Errorf("claim pending approval: %w", err)
		}
		approval = claimed
		return nil
	})
	if errors.Is(err, ErrApprovalExpired) {
		markExpired(ctx, pool, firmID, approvalID)
	}
	if err != nil {
		return PendingApproval{}, err
	}

	if err := ExecuteTransition(ctx, pool, firmID, userID, approval.InstanceID, approval.ProposedActionKey, nil); err != nil {
		_ = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
			_, revertErr := tx.Exec(ctx, `
				UPDATE pending_approvals SET status = 'pending', resolved_by = NULL, resolved_at = NULL WHERE id = $1
			`, approval.ID)
			return revertErr
		})
		return PendingApproval{}, err
	}
	return approval, nil
}

// Reject marks approvalID rejected and notifies whoever originally
// requested it - a single transaction (unlike Approve, there is no
// external side effect to sequence around here).
func Reject(ctx context.Context, pool *pgxpool.Pool, firmID, userID, approvalID uuid.UUID) (PendingApproval, error) {
	var approval PendingApproval
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrApprovalNotFound
		}

		a, err := loadApprovalTx(ctx, tx, firmID, approvalID)
		if err != nil {
			return err
		}
		allowed, err := permission.Has(ctx, tx, firmID, userID, a.PermissionKey)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrPermissionDenied
		}
		if err := checkResolvable(a); err != nil {
			return err
		}

		row := tx.QueryRow(ctx, `
			UPDATE pending_approvals SET status = 'rejected', resolved_by = $1, resolved_at = now()
			WHERE id = $2
			RETURNING `+approvalColumns, userID, approvalID)
		resolved, err := scanPendingApproval(row)
		if err != nil {
			return fmt.Errorf("mark approval rejected: %w", err)
		}
		approval = resolved

		title := fmt.Sprintf("Approval rejected: %s", approval.ProposedActionKey)
		body := fmt.Sprintf("Your proposed action %q was rejected.", approval.ProposedActionKey)
		_, err = notification.CreateTx(ctx, tx, firmID, approval.RequestedBy, notificationTypeApprovalRejected, title, body, map[string]any{
			"instanceId":        approval.InstanceID.String(),
			"proposedActionKey": approval.ProposedActionKey,
			"pendingApprovalId": approval.ID.String(),
		})
		return err
	})
	if errors.Is(err, ErrApprovalExpired) {
		markExpired(ctx, pool, firmID, approvalID)
	}
	if err != nil {
		return PendingApproval{}, err
	}
	return approval, nil
}
