// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/notification"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// seedApprover seeds a second user in firmID under a distinct role key
// ("approver") - seedUserInFirm's own role key ("tester") is hardcoded,
// so every test in this file that needs two distinct users/roles in the
// same firm (a requester and an approver) needs this instead for its
// second call.
func seedApprover(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmID uuid.UUID, keycloakSubject string, grantedPermissions ...string) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test Approver").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO roles (firm_id, key, name) VALUES ($1, 'approver', 'Approver') RETURNING id`, firmID).Scan(&roleID); err != nil {
			return err
		}
		for _, key := range grantedPermissions {
			if _, err := tx.Exec(ctx, `INSERT INTO role_permissions (firm_id, role_id, permission_key) VALUES ($1, $2, $3)`, firmID, roleID, key); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed approver role/grants/membership: %v", err)
	}
	return userID
}

// These tests exercise the approval workflow (Open Points item 41,
// approvals.go): a non-autonomous rule match creates a real
// pending_approvals row plus notifications, Approve executes the
// proposed transition for real, Reject notifies the requester, and an
// expired approval can no longer be resolved. setupTest/seedUserInFirm
// are shared with workflow_integration_test.go (same package).

// seedNonAutonomousRecordSaleRule creates a TriggerOnCreate,
// non-autonomous rule proposing stock_to_sale's own "record_sale"
// transition whenever a new instance's payload carries
// needsApproval=true - the fixture every test below builds on.
func seedNonAutonomousRecordSaleRule(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID uuid.UUID) uuid.UUID {
	t.Helper()
	rule := workflow.Rule{
		FirmID:        firmID,
		DefinitionKey: workflow.StockToSaleKey,
		Name:          "Sale needs approval",
		Trigger:       workflow.TriggerOnCreate,
		ConditionTree: workflow.ExpressionNode{
			Type: workflow.ConditionField, Field: "needsApproval", Op: string(workflow.OpEq), Value: true,
		},
		Actions: []workflow.Action{
			{Type: workflow.ActionTransition, ActionKey: "record_sale"},
		},
		Autonomous: false,
		Enabled:    true,
	}
	ruleID, err := workflow.CreateRule(ctx, appPool, rule)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	return ruleID
}

func TestNonAutonomousRule_CreatesApprovalAndNotifiesPermissionHolders(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Approvals A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-approvals-requester", workflow.AddStockPermission)
	approverID := seedApprover(ctx, t, adminPool, appPool, firmID, "sub-approvals-approver", workflow.RecordSalePermission)

	seedNonAutonomousRecordSaleRule(ctx, t, appPool, firmID)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget", "needsApproval": true})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// The instance itself must NOT have moved - a non-autonomous match
	// never runs its actions inline.
	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.State.Key != "in_stock" {
		t.Fatalf("expected the instance to still be in_stock (no autonomous execution), got %q", state.State.Key)
	}

	// approverID (holds RecordSalePermission) sees the pending approval;
	// userID (only holds AddStockPermission) does not.
	approverApprovals, err := workflow.ListPendingApprovals(ctx, appPool, firmID, approverID)
	if err != nil {
		t.Fatalf("ListPendingApprovals (approver): %v", err)
	}
	if len(approverApprovals) != 1 {
		t.Fatalf("expected the approver to see exactly 1 pending approval, got %d", len(approverApprovals))
	}
	approval := approverApprovals[0]
	if approval.InstanceID != instanceID || approval.ProposedActionKey != "record_sale" || approval.Status != workflow.ApprovalStatusPending {
		t.Fatalf("unexpected approval: %+v", approval)
	}
	if approval.PermissionKey != workflow.RecordSalePermission {
		t.Fatalf("expected permissionKey %q, got %q", workflow.RecordSalePermission, approval.PermissionKey)
	}

	requesterApprovals, err := workflow.ListPendingApprovals(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("ListPendingApprovals (requester): %v", err)
	}
	if len(requesterApprovals) != 0 {
		t.Fatalf("expected the requester (no record_sale permission) to see 0 resolvable approvals, got %d", len(requesterApprovals))
	}

	// The approver was notified; the requester was not (only recipients
	// holding the gated permission are notified - see
	// createPendingApprovalAndNotify's own doc comment).
	approverNotifications, err := notification.ListForUser(ctx, appPool, firmID, approverID, notification.ListOptions{})
	if err != nil {
		t.Fatalf("ListForUser (approver): %v", err)
	}
	if len(approverNotifications.Notifications) != 1 || approverNotifications.Notifications[0].Type != "approval_pending" {
		t.Fatalf("expected the approver to have exactly 1 approval_pending notification, got %+v", approverNotifications.Notifications)
	}

	requesterNotifications, err := notification.ListForUser(ctx, appPool, firmID, userID, notification.ListOptions{})
	if err != nil {
		t.Fatalf("ListForUser (requester): %v", err)
	}
	if len(requesterNotifications.Notifications) != 0 {
		t.Fatalf("expected the requester to have 0 notifications at this point, got %+v", requesterNotifications.Notifications)
	}
}

func TestApprove_ExecutesTransitionAndMarksApproved(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Approvals B') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-approve-requester", workflow.AddStockPermission)
	approverID := seedApprover(ctx, t, adminPool, appPool, firmID, "sub-approve-approver", workflow.RecordSalePermission)

	seedNonAutonomousRecordSaleRule(ctx, t, appPool, firmID)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget", "needsApproval": true})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	approvals, err := workflow.ListPendingApprovals(ctx, appPool, firmID, approverID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("ListPendingApprovals: %v (len=%d)", err, len(approvals))
	}
	approvalID := approvals[0].ID

	approved, err := workflow.Approve(ctx, appPool, firmID, approverID, approvalID)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.Status != workflow.ApprovalStatusApproved {
		t.Fatalf("expected status approved, got %q", approved.Status)
	}
	if approved.ResolvedBy == nil || *approved.ResolvedBy != approverID {
		t.Fatalf("expected resolvedBy %s, got %+v", approverID, approved.ResolvedBy)
	}

	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.State.Key != "sold" {
		t.Fatalf("expected the instance to have actually moved to sold after Approve, got %q", state.State.Key)
	}

	// A second Approve attempt on the same (now-resolved) approval is
	// rejected, not silently re-executed.
	if _, err := workflow.Approve(ctx, appPool, firmID, approverID, approvalID); !errors.Is(err, workflow.ErrApprovalNotPending) {
		t.Fatalf("expected ErrApprovalNotPending on a second Approve, got %v", err)
	}
}

func TestReject_MarksRejectedAndNotifiesRequester(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Approvals C') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-reject-requester", workflow.AddStockPermission)
	approverID := seedApprover(ctx, t, adminPool, appPool, firmID, "sub-reject-approver", workflow.RecordSalePermission)

	seedNonAutonomousRecordSaleRule(ctx, t, appPool, firmID)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget", "needsApproval": true})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	approvals, err := workflow.ListPendingApprovals(ctx, appPool, firmID, approverID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("ListPendingApprovals: %v (len=%d)", err, len(approvals))
	}
	approvalID := approvals[0].ID

	rejected, err := workflow.Reject(ctx, appPool, firmID, approverID, approvalID)
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if rejected.Status != workflow.ApprovalStatusRejected {
		t.Fatalf("expected status rejected, got %q", rejected.Status)
	}

	// The instance never moved.
	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.State.Key != "in_stock" {
		t.Fatalf("expected the instance to remain in_stock after a reject, got %q", state.State.Key)
	}

	// The requester (userID) was notified of the rejection.
	requesterNotifications, err := notification.ListForUser(ctx, appPool, firmID, userID, notification.ListOptions{})
	if err != nil {
		t.Fatalf("ListForUser (requester): %v", err)
	}
	found := false
	for _, n := range requesterNotifications.Notifications {
		if n.Type == "approval_rejected" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the requester to have an approval_rejected notification, got %+v", requesterNotifications.Notifications)
	}

	if _, err := workflow.Reject(ctx, appPool, firmID, approverID, approvalID); !errors.Is(err, workflow.ErrApprovalNotPending) {
		t.Fatalf("expected ErrApprovalNotPending resolving an already-rejected approval again, got %v", err)
	}
}

func TestApproval_ExpiredCannotBeResolved(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Approvals D') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-expire-requester", workflow.AddStockPermission)
	approverID := seedApprover(ctx, t, adminPool, appPool, firmID, "sub-expire-approver", workflow.RecordSalePermission)

	seedNonAutonomousRecordSaleRule(ctx, t, appPool, firmID)

	_, err = workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget", "needsApproval": true})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	approvals, err := workflow.ListPendingApprovals(ctx, appPool, firmID, approverID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("ListPendingApprovals: %v (len=%d)", err, len(approvals))
	}
	approvalID := approvals[0].ID

	// Backdate expires_at into the past, as the admin/superuser role
	// (bypasses RLS) - simulating real time having passed rather than
	// waiting DefaultApprovalTTL out for real.
	if _, err := adminPool.Exec(ctx, `UPDATE pending_approvals SET expires_at = $1 WHERE id = $2`, time.Now().Add(-time.Hour), approvalID); err != nil {
		t.Fatalf("backdate expires_at: %v", err)
	}

	if _, err := workflow.Approve(ctx, appPool, firmID, approverID, approvalID); !errors.Is(err, workflow.ErrApprovalExpired) {
		t.Fatalf("expected ErrApprovalExpired, got %v", err)
	}

	// The lazy expiry means it's no longer even listed as pending.
	approvalsAfter, err := workflow.ListPendingApprovals(ctx, appPool, firmID, approverID)
	if err != nil {
		t.Fatalf("ListPendingApprovals after expiry: %v", err)
	}
	if len(approvalsAfter) != 0 {
		t.Fatalf("expected 0 pending approvals after expiry, got %d", len(approvalsAfter))
	}

	if _, err := workflow.Reject(ctx, appPool, firmID, approverID, approvalID); !errors.Is(err, workflow.ErrApprovalNotPending) {
		t.Fatalf("expected ErrApprovalNotPending rejecting an already-expired approval, got %v", err)
	}
}
