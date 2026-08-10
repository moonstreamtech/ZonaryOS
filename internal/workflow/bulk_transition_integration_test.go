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

	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// TestBulkExecuteTransition_AllSucceed confirms the ordinary case: every
// instance given, all in the same definition and the same compatible
// "open" state, moves together to "in_progress" via the "start" action.
func TestBulkExecuteTransition_AllSucceed(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Bulk Firm') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedTaskApprovalWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedTaskApprovalWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "bulk-user", workflow.CreateTaskPermission, workflow.StartTaskPermission)

	var instanceIDs []uuid.UUID
	for i := 0; i < 3; i++ {
		id, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{})
		if err != nil {
			t.Fatalf("CreateInstance: %v", err)
		}
		instanceIDs = append(instanceIDs, id)
	}

	result, err := workflow.BulkExecuteTransition(ctx, appPool, firmID, userID, instanceIDs, "start", nil)
	if err != nil {
		t.Fatalf("BulkExecuteTransition: %v", err)
	}
	if result.ToStateKey != "in_progress" {
		t.Fatalf("expected to_state in_progress, got %q", result.ToStateKey)
	}
	for _, id := range instanceIDs {
		state, err := workflow.CurrentState(ctx, appPool, firmID, userID, id)
		if err != nil {
			t.Fatalf("CurrentState: %v", err)
		}
		if state.State.Key != "in_progress" {
			t.Errorf("instance %s: expected in_progress, got %q", id, state.State.Key)
		}
	}
}

// TestBulkExecuteTransition_PartialFailureRollsBackAll is Part 4's own
// explicit atomicity requirement: if ANY instance in the batch can't take
// the action (here, one instance was already moved to "in_progress"
// beforehand, so "start" no longer applies to it - ErrNoSuchTransition),
// the whole call fails and every OTHER instance that would have
// succeeded must remain completely unchanged - no partial commit.
func TestBulkExecuteTransition_PartialFailureRollsBackAll(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Bulk Firm') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedTaskApprovalWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedTaskApprovalWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "bulk-user", workflow.CreateTaskPermission, workflow.StartTaskPermission)

	goodA, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{})
	if err != nil {
		t.Fatalf("CreateInstance goodA: %v", err)
	}
	goodB, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{})
	if err != nil {
		t.Fatalf("CreateInstance goodB: %v", err)
	}
	alreadyStarted, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{})
	if err != nil {
		t.Fatalf("CreateInstance alreadyStarted: %v", err)
	}
	// Move this one out of "open" ahead of time, so "start" no longer
	// applies to it when the bulk call runs.
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, alreadyStarted, "start", nil); err != nil {
		t.Fatalf("pre-move alreadyStarted: %v", err)
	}

	_, err = workflow.BulkExecuteTransition(ctx, appPool, firmID, userID, []uuid.UUID{goodA, goodB, alreadyStarted}, "start", nil)
	if !errors.Is(err, workflow.ErrNoSuchTransition) {
		t.Fatalf("expected ErrNoSuchTransition, got %v", err)
	}

	// goodA and goodB must still be "open" - not partially transitioned.
	for _, id := range []uuid.UUID{goodA, goodB} {
		state, err := workflow.CurrentState(ctx, appPool, firmID, userID, id)
		if err != nil {
			t.Fatalf("CurrentState: %v", err)
		}
		if state.State.Key != "open" {
			t.Errorf("instance %s: expected the failed bulk call to leave it at open, got %q", id, state.State.Key)
		}
	}
}

// TestBulkExecuteTransition_MixedDefinitionsRejected confirms the
// "must all be the same definition" precondition is checked before any
// instance is touched.
func TestBulkExecuteTransition_MixedDefinitionsRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Bulk Firm') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	taskDefID, err := workflow.SeedTaskApprovalWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedTaskApprovalWorkflow: %v", err)
	}
	saleDefID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "bulk-user",
		workflow.CreateTaskPermission, workflow.StartTaskPermission, workflow.AddStockPermission)

	taskInstance, err := workflow.CreateInstance(ctx, appPool, firmID, userID, taskDefID, map[string]any{})
	if err != nil {
		t.Fatalf("CreateInstance (task): %v", err)
	}
	saleInstance, err := workflow.CreateInstance(ctx, appPool, firmID, userID, saleDefID, map[string]any{})
	if err != nil {
		t.Fatalf("CreateInstance (sale): %v", err)
	}

	_, err = workflow.BulkExecuteTransition(ctx, appPool, firmID, userID, []uuid.UUID{taskInstance, saleInstance}, "start", nil)
	if !errors.Is(err, workflow.ErrBulkTransitionMixedDefinitions) {
		t.Fatalf("expected ErrBulkTransitionMixedDefinitions, got %v", err)
	}
}

func TestBulkExecuteTransition_EmptyRejected(t *testing.T) {
	_, appPool := setupTest(t)
	_, err := workflow.BulkExecuteTransition(context.Background(), appPool, uuid.New(), uuid.New(), nil, "start", nil)
	if !errors.Is(err, workflow.ErrBulkTransitionEmpty) {
		t.Fatalf("expected ErrBulkTransitionEmpty, got %v", err)
	}
}
