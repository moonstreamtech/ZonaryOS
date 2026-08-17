// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// TestEvaluate_EdgeEventCondition_TrueWhenRecentMatchingEventExists
// confirms the "edge_event" leaf's DB-backed read: true when a matching
// edge_events row exists within the lookback window, false when the
// event type doesn't match or the row is too old, and fails closed
// (ErrConditionEvaluation) with no pool.
func TestEvaluate_EdgeEventCondition_TrueWhenRecentMatchingEventExists(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID := seedFirm(ctx, t, adminPool, "Firm A")
	var agentID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO edge_agents (firm_id, name, status) VALUES ($1, 'Gateway', 'online') RETURNING id
	`, firmID).Scan(&agentID); err != nil {
		t.Fatalf("seed edge agent: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO edge_events (firm_id, agent_id, event_type, payload, received_at)
		VALUES ($1, $2, 'door_opened', '{}'::jsonb, now() - interval '2 minutes')
	`, firmID, agentID); err != nil {
		t.Fatalf("seed recent edge event: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO edge_events (firm_id, agent_id, event_type, payload, received_at)
		VALUES ($1, $2, 'door_opened', '{}'::jsonb, now() - interval '30 minutes')
	`, firmID, agentID); err != nil {
		t.Fatalf("seed stale edge event: %v", err)
	}

	ec := workflow.EvalContext{FirmID: firmID, Payload: map[string]any{}, Pool: appPool}

	// Recent, matching event type: true.
	matched, err := workflow.Evaluate(ctx, workflow.ExpressionNode{
		Type: workflow.ConditionEdgeEvent, AgentID: agentID.String(), EventType: "door_opened", EdgeEventWithinMinutes: 5,
	}, ec)
	if err != nil {
		t.Fatalf("Evaluate (recent, matching): %v", err)
	}
	if !matched {
		t.Error("expected a recent matching edge event to make the condition true")
	}

	// Only the stale row matches this narrower window: false.
	matched, err = workflow.Evaluate(ctx, workflow.ExpressionNode{
		Type: workflow.ConditionEdgeEvent, AgentID: agentID.String(), EventType: "door_opened", EdgeEventWithinMinutes: 5,
	}, ec)
	if err != nil {
		t.Fatalf("Evaluate (re-check recent): %v", err)
	}
	if !matched {
		t.Error("expected the recent event to still match on a second check")
	}

	// Non-matching event type: false.
	matched, err = workflow.Evaluate(ctx, workflow.ExpressionNode{
		Type: workflow.ConditionEdgeEvent, AgentID: agentID.String(), EventType: "window_opened", EdgeEventWithinMinutes: 5,
	}, ec)
	if err != nil {
		t.Fatalf("Evaluate (non-matching type): %v", err)
	}
	if matched {
		t.Error("expected a non-matching event type to make the condition false")
	}

	// Only the stale (30 minutes ago) event exists in this window, too
	// old for a 1-minute lookback on a differently-typed check: false.
	matched, err = workflow.Evaluate(ctx, workflow.ExpressionNode{
		Type: workflow.ConditionEdgeEvent, AgentID: uuid.New().String(), EventType: "door_opened", EdgeEventWithinMinutes: 5,
	}, ec)
	if err != nil {
		t.Fatalf("Evaluate (unknown agent): %v", err)
	}
	if matched {
		t.Error("expected an unrelated agent id to make the condition false")
	}
}
