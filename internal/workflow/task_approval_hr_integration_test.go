// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/hr"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// TestCreateInstance_TaskApprovalAssigneeValidation confirms
// FieldTypePerson's cross-module validation (checkPersonField, engine.go):
// a real person.ID is accepted, a nonexistent one is rejected, and
// omitting assignee_person_id entirely still succeeds (it's optional -
// see TaskApprovalSpec's own doc comment for why).
func TestCreateInstance_TaskApprovalAssigneeValidation(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Task HR') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedTaskApprovalWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedTaskApprovalWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "task-hr-user", workflow.CreateTaskPermission)

	// Inserted directly via adminPool (which, like every table owner,
	// bypasses RLS) rather than through the owner-gated hr.CreatePerson -
	// seedUserInFirm's own role is a plain member, not owner-flagged, and
	// this test is about the workflow engine's cross-module validation,
	// not HR's own authorization (already covered by internal/hr's own
	// integration tests).
	personID := uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO people (id, firm_id, full_name, type) VALUES ($1, $2, 'Assignee One', 'employee')
	`, personID, firmID); err != nil {
		t.Fatalf("seed person: %v", err)
	}
	person := hr.Person{ID: personID}

	// A real person's ID is accepted.
	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"assignee_person_id": person.ID.String(),
	})
	if err != nil {
		t.Fatalf("CreateInstance with a real assignee: %v", err)
	}
	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.Payload["assignee_person_id"] != person.ID.String() {
		t.Errorf("expected the assignee to be stored in the payload, got %v", state.Payload)
	}

	// A nonexistent person ID is rejected.
	_, err = workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"assignee_person_id": uuid.New().String(),
	})
	if !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for a nonexistent assignee, got %v", err)
	}

	// Omitting the field entirely still succeeds - it's optional.
	if _, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{}); err != nil {
		t.Fatalf("CreateInstance with no assignee: %v", err)
	}
}
