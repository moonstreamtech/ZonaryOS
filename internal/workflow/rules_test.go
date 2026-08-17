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

// These are Evaluate's pure unit tests - no database. Cross-workflow
// "state" leaf evaluation needs a real Postgres connection (it reads
// another instance's row) - that path is covered by
// rules_integration_test.go instead; this file only covers the
// no-pool-configured error path for that leaf type.

func fieldLeaf(field string, op workflow.CompareOp, value any) workflow.ExpressionNode {
	return workflow.ExpressionNode{Type: workflow.ConditionField, Field: field, Op: string(op), Value: value}
}

func TestEvaluate_FieldCondition_AllOperators(t *testing.T) {
	ctx := context.Background()
	payload := map[string]any{"quantity": float64(5), "name": "widget"}
	ec := workflow.EvalContext{Payload: payload}

	cases := []struct {
		name string
		node workflow.ExpressionNode
		want bool
	}{
		{"eq true", fieldLeaf("quantity", workflow.OpEq, float64(5)), true},
		{"eq false", fieldLeaf("quantity", workflow.OpEq, float64(6)), false},
		{"neq true", fieldLeaf("quantity", workflow.OpNeq, float64(6)), true},
		{"lt true", fieldLeaf("quantity", workflow.OpLt, float64(10)), true},
		{"lt false", fieldLeaf("quantity", workflow.OpLt, float64(1)), false},
		{"gt true", fieldLeaf("quantity", workflow.OpGt, float64(1)), true},
		{"lte true (equal)", fieldLeaf("quantity", workflow.OpLte, float64(5)), true},
		{"gte true (equal)", fieldLeaf("quantity", workflow.OpGte, float64(5)), true},
		{"contains true", fieldLeaf("name", workflow.OpContains, "id"), true},
		{"contains false", fieldLeaf("name", workflow.OpContains, "zzz"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := workflow.Evaluate(ctx, tc.node, ec)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if got != tc.want {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestEvaluate_FieldCondition_MissingFieldFailsClosed(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{Payload: map[string]any{}}
	_, err := workflow.Evaluate(ctx, fieldLeaf("missing", workflow.OpEq, "x"), ec)
	if !errors.Is(err, workflow.ErrConditionEvaluation) {
		t.Fatalf("expected ErrConditionEvaluation, got %v", err)
	}
}

func TestEvaluate_FieldCompareCondition(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{Payload: map[string]any{"quantity": float64(5), "reorderLevel": float64(3)}}
	node := workflow.ExpressionNode{
		Type:   workflow.ConditionFieldCompare,
		FieldA: "quantity",
		Op:     string(workflow.OpGt),
		FieldB: "reorderLevel",
	}
	got, err := workflow.Evaluate(ctx, node, ec)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !got {
		t.Error("expected quantity > reorderLevel to hold")
	}
}

func TestEvaluate_FieldCompareCondition_MissingFieldFailsClosed(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{Payload: map[string]any{"quantity": float64(5)}}
	node := workflow.ExpressionNode{Type: workflow.ConditionFieldCompare, FieldA: "quantity", Op: string(workflow.OpGt), FieldB: "reorderLevel"}
	if _, err := workflow.Evaluate(ctx, node, ec); !errors.Is(err, workflow.ErrConditionEvaluation) {
		t.Fatalf("expected ErrConditionEvaluation, got %v", err)
	}
}

func TestEvaluate_StateCondition_NoPoolFailsClosed(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{
		FirmID:  uuid.New(),
		Payload: map[string]any{"purchaseOrderId": uuid.New().String()},
		// Pool deliberately left nil.
	}
	node := workflow.ExpressionNode{
		Type:            workflow.ConditionState,
		DefinitionKey:   "purchase_order",
		InstanceIDField: "purchaseOrderId",
		State:           "received",
	}
	if _, err := workflow.Evaluate(ctx, node, ec); !errors.Is(err, workflow.ErrConditionEvaluation) {
		t.Fatalf("expected ErrConditionEvaluation, got %v", err)
	}
}

func TestEvaluate_StateCondition_NonUUIDReferenceFailsClosed(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{
		FirmID:  uuid.New(),
		Payload: map[string]any{"purchaseOrderId": "not-a-uuid"},
	}
	node := workflow.ExpressionNode{
		Type:            workflow.ConditionState,
		DefinitionKey:   "purchase_order",
		InstanceIDField: "purchaseOrderId",
		State:           "received",
	}
	if _, err := workflow.Evaluate(ctx, node, ec); !errors.Is(err, workflow.ErrConditionEvaluation) {
		t.Fatalf("expected ErrConditionEvaluation, got %v", err)
	}
}

func TestEvaluate_EdgeEventCondition_MissingFieldsFailClosed(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{FirmID: uuid.New(), Payload: map[string]any{}}

	cases := []workflow.ExpressionNode{
		{Type: workflow.ConditionEdgeEvent, EventType: "door_opened", EdgeEventWithinMinutes: 5},                        // missing AgentID
		{Type: workflow.ConditionEdgeEvent, AgentID: uuid.New().String(), EdgeEventWithinMinutes: 5},                    // missing EventType
		{Type: workflow.ConditionEdgeEvent, AgentID: uuid.New().String(), EventType: "door_opened"},                     // missing/zero EdgeEventWithinMinutes
		{Type: workflow.ConditionEdgeEvent, AgentID: "not-a-uuid", EventType: "door_opened", EdgeEventWithinMinutes: 5}, // malformed AgentID
	}
	for i, node := range cases {
		if _, err := workflow.Evaluate(ctx, node, ec); !errors.Is(err, workflow.ErrInvalidRule) && !errors.Is(err, workflow.ErrConditionEvaluation) {
			t.Errorf("case %d: expected ErrInvalidRule or ErrConditionEvaluation, got %v", i, err)
		}
	}
}

func TestEvaluate_EdgeEventCondition_NoPoolFailsClosed(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{FirmID: uuid.New(), Payload: map[string]any{}}
	node := workflow.ExpressionNode{
		Type: workflow.ConditionEdgeEvent, AgentID: uuid.New().String(), EventType: "door_opened", EdgeEventWithinMinutes: 5,
	}
	if _, err := workflow.Evaluate(ctx, node, ec); !errors.Is(err, workflow.ErrConditionEvaluation) {
		t.Fatalf("expected ErrConditionEvaluation, got %v", err)
	}
}

func TestValidateExpressionTree_RejectsMalformedEdgeEventLeaf(t *testing.T) {
	cases := []workflow.ExpressionNode{
		{Type: workflow.ConditionEdgeEvent, EventType: "door_opened", EdgeEventWithinMinutes: 5},
		{Type: workflow.ConditionEdgeEvent, AgentID: "not-a-uuid", EventType: "door_opened", EdgeEventWithinMinutes: 5},
	}
	for i, node := range cases {
		if err := workflow.ValidateExpressionTree(node); !errors.Is(err, workflow.ErrInvalidRule) {
			t.Errorf("case %d: expected ErrInvalidRule, got %v", i, err)
		}
	}

	valid := workflow.ExpressionNode{Type: workflow.ConditionEdgeEvent, AgentID: uuid.New().String(), EventType: "door_opened", EdgeEventWithinMinutes: 5}
	if err := workflow.ValidateExpressionTree(valid); err != nil {
		t.Errorf("expected a well-formed edge_event leaf to validate, got %v", err)
	}
}

func TestValidateRuleShape_OnEdgeEventRejectsNonNotifyActions(t *testing.T) {
	rule := workflow.Rule{
		Name: "test", DefinitionKey: "sensor_watch", Trigger: workflow.TriggerOnEdgeEvent,
		ConditionTree: workflow.ExpressionNode{Type: workflow.ConditionField, Field: "tempC", Op: string(workflow.OpGt), Value: 30.0},
		Actions:       []workflow.Action{{Type: workflow.ActionTransition, ActionKey: "close"}},
	}
	// validateRuleShape runs before any database access - passing a nil
	// pool is safe here since a rejected rule never reaches it (see
	// CreateRule's own doc comment).
	if _, err := workflow.CreateRule(context.Background(), nil, rule); !errors.Is(err, workflow.ErrInvalidRule) {
		t.Fatalf("expected an on_edge_event rule with a transition action to be rejected, got %v", err)
	}
}

func TestEvaluate_And(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{Payload: map[string]any{"a": float64(1), "b": float64(2)}}
	node := workflow.ExpressionNode{
		Op: string(workflow.LogicAnd),
		Children: []workflow.ExpressionNode{
			fieldLeaf("a", workflow.OpEq, float64(1)),
			fieldLeaf("b", workflow.OpEq, float64(2)),
		},
	}
	got, err := workflow.Evaluate(ctx, node, ec)
	if err != nil || !got {
		t.Fatalf("expected true, nil; got %v, %v", got, err)
	}

	node.Children[1] = fieldLeaf("b", workflow.OpEq, float64(999))
	got, err = workflow.Evaluate(ctx, node, ec)
	if err != nil || got {
		t.Fatalf("expected false, nil; got %v, %v", got, err)
	}
}

func TestEvaluate_And_ShortCircuitsBeforeALaterErroringChild(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{Payload: map[string]any{"a": float64(1)}}
	node := workflow.ExpressionNode{
		Op: string(workflow.LogicAnd),
		Children: []workflow.ExpressionNode{
			fieldLeaf("a", workflow.OpEq, float64(999)), // false, evaluated first
			fieldLeaf("missing", workflow.OpEq, "x"),    // would error if reached
		},
	}
	got, err := workflow.Evaluate(ctx, node, ec)
	if err != nil {
		t.Fatalf("expected AND to short-circuit before the erroring child, got err: %v", err)
	}
	if got {
		t.Error("expected false")
	}
}

func TestEvaluate_Or(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{Payload: map[string]any{"a": float64(1)}}
	node := workflow.ExpressionNode{
		Op: string(workflow.LogicOr),
		Children: []workflow.ExpressionNode{
			fieldLeaf("a", workflow.OpEq, float64(999)),
			fieldLeaf("a", workflow.OpEq, float64(1)),
		},
	}
	got, err := workflow.Evaluate(ctx, node, ec)
	if err != nil || !got {
		t.Fatalf("expected true, nil; got %v, %v", got, err)
	}
}

func TestEvaluate_Not(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{Payload: map[string]any{"a": float64(1)}}
	node := workflow.ExpressionNode{
		Op:       string(workflow.LogicNot),
		Children: []workflow.ExpressionNode{fieldLeaf("a", workflow.OpEq, float64(1))},
	}
	got, err := workflow.Evaluate(ctx, node, ec)
	if err != nil || got {
		t.Fatalf("expected false (NOT true), nil; got %v, %v", got, err)
	}
}

func TestEvaluate_NestedAndOrNot(t *testing.T) {
	ctx := context.Background()
	// (a == 1 AND NOT (b == 2)) OR (c == 3)
	ec := workflow.EvalContext{Payload: map[string]any{"a": float64(1), "b": float64(99), "c": float64(0)}}
	node := workflow.ExpressionNode{
		Op: string(workflow.LogicOr),
		Children: []workflow.ExpressionNode{
			{
				Op: string(workflow.LogicAnd),
				Children: []workflow.ExpressionNode{
					fieldLeaf("a", workflow.OpEq, float64(1)),
					{Op: string(workflow.LogicNot), Children: []workflow.ExpressionNode{fieldLeaf("b", workflow.OpEq, float64(2))}},
				},
			},
			fieldLeaf("c", workflow.OpEq, float64(3)),
		},
	}
	got, err := workflow.Evaluate(ctx, node, ec)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !got {
		t.Error("expected true")
	}
}

func TestEvaluate_LogicNode_StructuralErrors(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{Payload: map[string]any{}}

	cases := []struct {
		name string
		node workflow.ExpressionNode
	}{
		{"and with no children", workflow.ExpressionNode{Op: string(workflow.LogicAnd)}},
		{"or with no children", workflow.ExpressionNode{Op: string(workflow.LogicOr)}},
		{"not with zero children", workflow.ExpressionNode{Op: string(workflow.LogicNot)}},
		{"not with two children", workflow.ExpressionNode{
			Op:       string(workflow.LogicNot),
			Children: []workflow.ExpressionNode{fieldLeaf("a", workflow.OpEq, 1), fieldLeaf("b", workflow.OpEq, 2)},
		}},
		{"unknown logic op", workflow.ExpressionNode{Op: "xor", Children: []workflow.ExpressionNode{fieldLeaf("a", workflow.OpEq, 1)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := workflow.Evaluate(ctx, tc.node, ec); !errors.Is(err, workflow.ErrInvalidRule) {
				t.Fatalf("expected ErrInvalidRule, got %v", err)
			}
		})
	}
}

func TestEvaluate_UnknownConditionLeafType(t *testing.T) {
	ctx := context.Background()
	ec := workflow.EvalContext{Payload: map[string]any{}}
	node := workflow.ExpressionNode{Type: "bogus"}
	if _, err := workflow.Evaluate(ctx, node, ec); !errors.Is(err, workflow.ErrInvalidRule) {
		t.Fatalf("expected ErrInvalidRule, got %v", err)
	}
}

func TestValidateActions(t *testing.T) {
	valid := []workflow.Action{
		{Type: workflow.ActionTransition, ActionKey: "send"},
		{Type: workflow.ActionNotify, Channel: workflow.NotifyChannelAuditLog, MessageTemplate: "hello {{name}}"},
		{Type: workflow.ActionSetField, Field: "status", Value: "urgent"},
	}
	if err := workflow.ValidateActions(valid); err != nil {
		t.Fatalf("expected valid actions to pass, got: %v", err)
	}

	invalidCases := [][]workflow.Action{
		{{Type: workflow.ActionTransition}},                                      // missing actionKey
		{{Type: workflow.ActionNotify, MessageTemplate: "x"}},                    // missing/wrong channel
		{{Type: workflow.ActionNotify, Channel: workflow.NotifyChannelAuditLog}}, // missing messageTemplate
		{{Type: workflow.ActionSetField}},                                        // missing field
		{{Type: "bogus"}},                                                        // unknown type
	}
	for i, actions := range invalidCases {
		if err := workflow.ValidateActions(actions); !errors.Is(err, workflow.ErrInvalidRule) {
			t.Errorf("case %d: expected ErrInvalidRule, got %v", i, err)
		}
	}
}
