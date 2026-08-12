// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package absence_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/absence"
	"github.com/moonstreamtech/ZonaryOS/internal/hr"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// These tests exercise absence requests against a real Postgres instance -
// RLS isolation, permission enforcement, and the real workflow-engine-
// backed approval mechanism can't be meaningfully faked, same convention
// as every other package's integration tests in this codebase. Skipped
// unless both ZONARYOS_TEST_ADMIN_DATABASE_URL and
// ZONARYOS_TEST_APP_DATABASE_URL are set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run absence integration tests")
	}
	return adminDSN, appDSN
}

func setupTest(t *testing.T) (adminPool, appPool *pgxpool.Pool) {
	t.Helper()
	adminDSN, appDSN := testPools(t)
	ctx := context.Background()

	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	adminPool, err := zdb.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)
	if _, err := adminPool.Exec(ctx, `
		TRUNCATE firms, users, roles, role_permissions, user_firm_roles,
			people, contracts, absences, workflow_definitions, workflow_states, workflow_transitions,
			workflow_instances, pending_approvals, notifications,
			audit_log, permissions CASCADE
	`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err = zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(appPool.Close)

	return adminPool, appPool
}

// seedOwnerWithAbsenceApproval seeds a firm with an owner-flagged role
// holding the owner as its member, seeds the real
// workflow.AbsenceApprovalSpec workflow for that firm (via the plain
// pool-based SeedAbsenceApprovalWorkflow, which - unlike the *Tx variant
// internal/wizard.CreateDefaultFirm uses - does NOT auto-grant any
// permission to any role, see DefineWorkflow's own doc comment), and
// grants the owner role every one of that workflow's permission keys
// directly - mirroring internal/workflow/approvals_integration_test.go's
// own seedApprover fixture shape.
func seedOwnerWithAbsenceApproval(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmName, keycloakSubject string) (firmID, ownerID uuid.UUID) {
	t.Helper()

	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ($1) RETURNING id`, firmName).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test Owner").Scan(&ownerID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if _, err := workflow.SeedAbsenceApprovalWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed absence approval workflow: %v", err)
	}

	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (firm_id, key, name, is_owner) VALUES ($1, 'owner', 'Owner', true) RETURNING id
		`, firmID).Scan(&roleID); err != nil {
			return err
		}
		for _, key := range []string{workflow.RequestAbsencePermission, workflow.ApproveAbsencePermission, workflow.RejectAbsencePermission} {
			if _, err := tx.Exec(ctx, `INSERT INTO role_permissions (firm_id, role_id, permission_key) VALUES ($1, $2, $3)`, firmID, roleID, key); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, ownerID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed owner role/grants/membership: %v", err)
	}
	return firmID, ownerID
}

func seedPerson(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID, ownerID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	p, err := hr.CreatePerson(ctx, appPool, firmID, ownerID, hr.CreatePersonInput{FullName: name, Type: hr.PersonTypeEmployee})
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}
	return p.ID
}

// TestCreateAbsence_CreatesPendingApprovalAndNotification is the HR depth
// batch's own required test case: submitting an absence creates a
// pending_approvals row and a notification.
func TestCreateAbsence_CreatesPendingApprovalAndNotification(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwnerWithAbsenceApproval(ctx, t, adminPool, appPool, "Firm Absence", "absence-owner")
	person := seedPerson(ctx, t, appPool, firmID, ownerID, "Ada Lovelace")

	abs, err := absence.CreateAbsence(ctx, appPool, firmID, ownerID, absence.CreateAbsenceInput{
		PersonID: person, Type: absence.TypeVacation, StartDate: "2026-09-01", EndDate: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("CreateAbsence: %v", err)
	}
	if abs.Status != absence.StatusPending {
		t.Fatalf("expected status pending, got %q", abs.Status)
	}
	if abs.WorkflowInstanceID == nil {
		t.Fatal("expected a non-nil WorkflowInstanceID after CreateAbsence")
	}

	var pendingCount int
	if err := adminPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM pending_approvals WHERE instance_id = $1 AND status = 'pending' AND proposed_action_key = 'approve'
	`, *abs.WorkflowInstanceID).Scan(&pendingCount); err != nil {
		t.Fatalf("count pending_approvals: %v", err)
	}
	if pendingCount != 1 {
		t.Fatalf("expected exactly 1 pending pending_approvals row for this instance, got %d", pendingCount)
	}

	var notificationCount int
	if err := adminPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM notifications WHERE firm_id = $1 AND type = 'approval_pending' AND recipient_user_id = $2
	`, firmID, ownerID).Scan(&notificationCount); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	if notificationCount != 1 {
		t.Fatalf("expected exactly 1 approval_pending notification for the owner (the only holder of %q), got %d", workflow.ApproveAbsencePermission, notificationCount)
	}
}

// TestReject_UpdatesAbsenceStatusToRejected is the HR depth batch's own
// required test case: owner reject updates absences.status to
// 'rejected'.
func TestReject_UpdatesAbsenceStatusToRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwnerWithAbsenceApproval(ctx, t, adminPool, appPool, "Firm Absence B", "absence-owner-2")
	person := seedPerson(ctx, t, appPool, firmID, ownerID, "Alan Turing")

	abs, err := absence.CreateAbsence(ctx, appPool, firmID, ownerID, absence.CreateAbsenceInput{
		PersonID: person, Type: absence.TypeSick, StartDate: "2026-09-10", EndDate: "2026-09-11",
	})
	if err != nil {
		t.Fatalf("CreateAbsence: %v", err)
	}

	rejected, err := absence.Reject(ctx, appPool, firmID, ownerID, abs.ID)
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if rejected.Status != absence.StatusRejected {
		t.Fatalf("expected status rejected, got %q", rejected.Status)
	}
	if rejected.ApprovedBy == nil || *rejected.ApprovedBy != ownerID {
		t.Fatalf("expected ApprovedBy to be the rejecting owner, got %v", rejected.ApprovedBy)
	}

	fetched, err := absence.GetAbsence(ctx, appPool, firmID, ownerID, abs.ID)
	if err != nil {
		t.Fatalf("GetAbsence: %v", err)
	}
	if fetched.Status != absence.StatusRejected {
		t.Fatalf("expected persisted status rejected, got %q", fetched.Status)
	}

	// A second reject must fail - the absence is no longer pending.
	if _, err := absence.Reject(ctx, appPool, firmID, ownerID, abs.ID); !errors.Is(err, absence.ErrAbsenceNotPending) {
		t.Fatalf("expected ErrAbsenceNotPending on a second reject, got %v", err)
	}
}
