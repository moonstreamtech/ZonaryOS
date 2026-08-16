// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package project_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/project"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// These tests exercise internal/project against a real Postgres instance
// - RLS isolation and permission enforcement can't be meaningfully faked,
// same convention every other package's integration tests in this
// codebase follow. Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL
// and ZONARYOS_TEST_APP_DATABASE_URL are set.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run project integration tests")
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
			projects, people, contracts, time_entries,
			workflow_definitions, workflow_states, workflow_transitions, workflow_instances,
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

func seedOwner(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmName, keycloakSubject string) (firmID, userID, roleID uuid.UUID) {
	t.Helper()

	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ($1) RETURNING id`, firmName).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test Owner").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (firm_id, key, name, is_owner) VALUES ($1, 'owner', 'Owner', true) RETURNING id
		`, firmID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed owner role/membership: %v", err)
	}
	return firmID, userID, roleID
}

func seedPerson(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID) uuid.UUID {
	t.Helper()
	var personID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO people (firm_id, full_name, type) VALUES ($1, 'Test Person', 'employee') RETURNING id
	`, firmID).Scan(&personID); err != nil {
		t.Fatalf("seed person: %v", err)
	}
	return personID
}

// TestGetProjectSummary_CountsTasksByStateAndLoggedHours confirms the
// summary counts tasks correctly across states (a completed task_approval
// instance counts too - the summary tracks totals, not just open work)
// and sums time_entries logged against the linked instances.
func TestGetProjectSummary_CountsTasksByStateAndLoggedHours(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, roleID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "summary-owner")
	personID := seedPerson(ctx, t, adminPool, firmID)

	// An hourly contract so EstimatedCost has something to compute from.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO contracts (firm_id, person_id, type, amount, start_date) VALUES ($1, $2, 'hourly', 50.00, now())
	`, firmID, personID); err != nil {
		t.Fatalf("seed contract: %v", err)
	}

	p, err := project.CreateProject(ctx, appPool, firmID, ownerID, project.CreateProjectInput{Name: "Website Redesign", Budget: "10000.00"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	var taskDefID uuid.UUID
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		id, err := workflow.SeedTaskApprovalWorkflowTx(ctx, tx, firmID, roleID)
		taskDefID = id
		return err
	})
	if err != nil {
		t.Fatalf("SeedTaskApprovalWorkflowTx: %v", err)
	}

	// Two tasks linked to the project: one left open, one completed.
	openTaskID, err := workflow.CreateInstance(ctx, appPool, firmID, ownerID, taskDefID, map[string]any{"project_id": p.ID.String()})
	if err != nil {
		t.Fatalf("CreateInstance (open task): %v", err)
	}
	doneTaskID, err := workflow.CreateInstance(ctx, appPool, firmID, ownerID, taskDefID, map[string]any{"project_id": p.ID.String()})
	if err != nil {
		t.Fatalf("CreateInstance (task to complete): %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, ownerID, doneTaskID, "start", nil); err != nil {
		t.Fatalf("ExecuteTransition start: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, ownerID, doneTaskID, "complete", nil); err != nil {
		t.Fatalf("ExecuteTransition complete: %v", err)
	}

	// A task NOT linked to this project - must not be counted.
	if _, err := workflow.CreateInstance(ctx, appPool, firmID, ownerID, taskDefID, map[string]any{}); err != nil {
		t.Fatalf("CreateInstance (unlinked task): %v", err)
	}

	// Time logged against both linked tasks.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO time_entries (firm_id, person_id, workflow_instance_id, created_by, date, hours)
		VALUES ($1, $2, $3, $4, now(), 5), ($1, $2, $5, $4, now(), 3)
	`, firmID, personID, openTaskID, ownerID, doneTaskID); err != nil {
		t.Fatalf("seed time entries: %v", err)
	}

	summary, err := project.GetProjectSummary(ctx, appPool, firmID, ownerID, p.ID)
	if err != nil {
		t.Fatalf("GetProjectSummary: %v", err)
	}
	if summary.TotalTasks != 2 {
		t.Errorf("expected 2 linked tasks, got %d", summary.TotalTasks)
	}
	if summary.TotalHoursLogged != "8.00" {
		t.Errorf("expected 8.00 total hours logged, got %q", summary.TotalHoursLogged)
	}
	if summary.Budget == nil || *summary.Budget != "10000.0000" {
		t.Errorf("expected budget 10000.0000, got %v", summary.Budget)
	}
	if summary.EstimatedCost == nil || *summary.EstimatedCost != "400.0000" {
		t.Errorf("expected estimated cost 8.00 * 50.00 = 400.0000, got %v", summary.EstimatedCost)
	}

	foundOpen, foundDone := false, false
	for _, c := range summary.TasksByState {
		if c.StateKey == "open" && c.Count == 1 {
			foundOpen = true
		}
		if c.StateKey == "done" && c.Count == 1 {
			foundDone = true
		}
	}
	if !foundOpen || !foundDone {
		t.Errorf("expected exactly 1 task in 'open' and 1 in 'done', got %+v", summary.TasksByState)
	}
}

// TestCreateInstance_TaskApprovalProjectValidation confirms
// FieldTypeProject's cross-module validation (projectValidator,
// validation.go): a real project.ID is accepted, a nonexistent one is
// rejected, and omitting project_id entirely still succeeds (it's
// optional).
func TestCreateInstance_TaskApprovalProjectValidation(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, roleID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "project-validation-owner")
	p, err := project.CreateProject(ctx, appPool, firmID, ownerID, project.CreateProjectInput{Name: "Real Project"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	var taskDefID uuid.UUID
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		id, err := workflow.SeedTaskApprovalWorkflowTx(ctx, tx, firmID, roleID)
		taskDefID = id
		return err
	})
	if err != nil {
		t.Fatalf("SeedTaskApprovalWorkflowTx: %v", err)
	}

	if _, err := workflow.CreateInstance(ctx, appPool, firmID, ownerID, taskDefID, map[string]any{"project_id": p.ID.String()}); err != nil {
		t.Fatalf("CreateInstance with a real project: %v", err)
	}

	_, err = workflow.CreateInstance(ctx, appPool, firmID, ownerID, taskDefID, map[string]any{"project_id": uuid.New().String()})
	if !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for a nonexistent project, got %v", err)
	}

	if _, err := workflow.CreateInstance(ctx, appPool, firmID, ownerID, taskDefID, map[string]any{}); err != nil {
		t.Fatalf("CreateInstance with no project: %v", err)
	}
}
