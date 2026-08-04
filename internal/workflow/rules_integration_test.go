// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// These tests exercise the rule engine's impure driver (EvaluateRules,
// wired into CreateInstance/ExecuteTransition - see rules.go) against a
// real Postgres instance: a rule's cross-workflow "state" condition reads
// another instance's row for real, and the fail-closed/pending-approval
// paths write real audit_log rows. setupTest/seedUserInFirm are shared
// with workflow_integration_test.go (same package).

func TestRuleEngine_FiresOnExecuteTransition(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-rules-transition",
		workflow.AddStockPermission, workflow.RecordSalePermission)

	// Rule: on record_sale, if quantity > 5, write an audit_log
	// notification.
	rule := workflow.Rule{
		FirmID:        firmID,
		DefinitionKey: workflow.StockToSaleKey,
		Name:          "High-quantity sale notice",
		Trigger:       workflow.TriggerOnTransition,
		ConditionTree: workflow.ExpressionNode{
			Type: workflow.ConditionField, Field: "quantity", Op: string(workflow.OpGt), Value: float64(5),
		},
		Actions: []workflow.Action{
			{Type: workflow.ActionNotify, Channel: workflow.NotifyChannelAuditLog, MessageTemplate: "sold {{quantity}} of {{item}}"},
		},
		Autonomous: true,
		Enabled:    true,
	}
	ruleID, err := workflow.CreateRule(ctx, appPool, rule)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget", "quantity": float64(10)})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", nil); err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}

	var count int
	var changesJSON []byte
	err = adminPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		WHERE firm_id = $1 AND entity_type = 'workflow_instance' AND entity_id = $2 AND action = 'notify'
	`, firmID, instanceID).Scan(&count)
	if err != nil {
		t.Fatalf("count notify audit entries: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 rule-notify audit_log entry, got %d", count)
	}
	if err := adminPool.QueryRow(ctx, `
		SELECT changes FROM audit_log
		WHERE firm_id = $1 AND entity_type = 'workflow_instance' AND entity_id = $2 AND action = 'notify'
	`, firmID, instanceID).Scan(&changesJSON); err != nil {
		t.Fatalf("query notify changes: %v", err)
	}
	var changes map[string]any
	if err := json.Unmarshal(changesJSON, &changes); err != nil {
		t.Fatalf("unmarshal changes: %v", err)
	}
	if changes["ruleId"] != ruleID.String() {
		t.Errorf("expected changes.ruleId %q, got %v", ruleID, changes["ruleId"])
	}
	if changes["message"] != "sold 10 of widget" {
		t.Errorf("expected rendered message %q, got %v", "sold 10 of widget", changes["message"])
	}

	// A rule that doesn't match (quantity not > 5) should not fire at all.
	instanceID2, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "gadget", "quantity": float64(1)})
	if err != nil {
		t.Fatalf("CreateInstance (low quantity): %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID2, "record_sale", nil); err != nil {
		t.Fatalf("ExecuteTransition (low quantity): %v", err)
	}
	var count2 int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		WHERE firm_id = $1 AND entity_type = 'workflow_instance' AND entity_id = $2 AND action = 'notify'
	`, firmID, instanceID2).Scan(&count2); err != nil {
		t.Fatalf("count notify audit entries (low quantity): %v", err)
	}
	if count2 != 0 {
		t.Errorf("expected no rule-notify entry for a non-matching instance, got %d", count2)
	}
}

// TestRuleEngine_CrossWorkflowStateCondition proves the "state" leaf's
// real cross-workflow read: a stock_to_sale instance references a real
// purchase_order instance by ID, and a rule's condition checks that
// referenced instance's actual current state (not a mock).
func TestRuleEngine_CrossWorkflowStateCondition(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules B') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	stockDefinitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed stock_to_sale: %v", err)
	}
	poDefinitionID, err := workflow.SeedPurchaseOrderWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed purchase_order: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-rules-cross-workflow",
		workflow.AddStockPermission, workflow.CreatePurchaseOrderPermission)

	poInstanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, poDefinitionID, map[string]any{"vendor": "Acme Supply Co"})
	if err != nil {
		t.Fatalf("create purchase order instance: %v", err)
	}

	rule := workflow.Rule{
		FirmID:        firmID,
		DefinitionKey: workflow.StockToSaleKey,
		Name:          "Cross-workflow draft-PO match",
		Trigger:       workflow.TriggerOnCreate,
		ConditionTree: workflow.ExpressionNode{
			Type: workflow.ConditionState, DefinitionKey: workflow.PurchaseOrderKey,
			InstanceIDField: "purchaseOrderId", State: "draft",
		},
		Actions: []workflow.Action{
			{Type: workflow.ActionSetField, Field: "crossWorkflowMatched", Value: true},
		},
		Autonomous: true,
		Enabled:    true,
	}
	if _, err := workflow.CreateRule(ctx, appPool, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	stockInstanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, stockDefinitionID, map[string]any{
		"item": "widget", "quantity": float64(1), "purchaseOrderId": poInstanceID.String(),
	})
	if err != nil {
		t.Fatalf("create stock instance: %v", err)
	}

	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, stockInstanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.Payload["crossWorkflowMatched"] != true {
		t.Errorf("expected the cross-workflow rule's set_field action to have run, got payload %v", state.Payload)
	}
}

// TestRuleEngine_ConditionEvaluationErrorFailsClosed: a rule referencing a
// nonexistent cross-workflow instance must NOT execute its actions, and
// must write an audit_log entry instead - the triggering CreateInstance/
// ExecuteTransition call itself must still succeed.
func TestRuleEngine_ConditionEvaluationErrorFailsClosed(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules C') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	if _, err := workflow.SeedPurchaseOrderWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed purchase_order: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-rules-fail-closed", workflow.AddStockPermission)

	rule := workflow.Rule{
		FirmID:        firmID,
		DefinitionKey: workflow.StockToSaleKey,
		Name:          "Broken cross-workflow rule",
		Trigger:       workflow.TriggerOnCreate,
		ConditionTree: workflow.ExpressionNode{
			Type: workflow.ConditionState, DefinitionKey: workflow.PurchaseOrderKey,
			InstanceIDField: "purchaseOrderId", State: "draft",
		},
		Actions: []workflow.Action{
			{Type: workflow.ActionSetField, Field: "shouldNeverBeSet", Value: true},
		},
		Autonomous: true,
		Enabled:    true,
	}
	ruleID, err := workflow.CreateRule(ctx, appPool, rule)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	// Payload has no purchaseOrderId field at all - the condition must
	// error, not just evaluate false.
	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget", "quantity": float64(1)})
	if err != nil {
		t.Fatalf("CreateInstance must still succeed despite the broken rule: %v", err)
	}

	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if _, present := state.Payload["shouldNeverBeSet"]; present {
		t.Errorf("expected the erroring rule's action to NOT have run, got payload %v", state.Payload)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		WHERE firm_id = $1 AND entity_type = 'workflow_rule' AND entity_id = $2 AND action = 'evaluation_error'
	`, firmID, ruleID).Scan(&count); err != nil {
		t.Fatalf("count evaluation_error audit entries: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 evaluation_error audit_log entry, got %d", count)
	}
}

// TestRuleEngine_NonAutonomousRuleRecordsPendingApprovalInsteadOfActing
// covers Never-Violate Rule 8's per-rule autonomous/human-approval toggle:
// a matching rule with Autonomous=false must not run its actions.
func TestRuleEngine_NonAutonomousRuleRecordsPendingApprovalInsteadOfActing(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules D') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-rules-pending-approval", workflow.AddStockPermission)

	rule := workflow.Rule{
		FirmID:        firmID,
		DefinitionKey: workflow.StockToSaleKey,
		Name:          "Requires approval",
		Trigger:       workflow.TriggerOnCreate,
		ConditionTree: workflow.ExpressionNode{Type: workflow.ConditionField, Field: "quantity", Op: string(workflow.OpGt), Value: float64(0)},
		Actions: []workflow.Action{
			{Type: workflow.ActionSetField, Field: "shouldNeverBeSet", Value: true},
		},
		Autonomous: false,
		Enabled:    true,
	}
	ruleID, err := workflow.CreateRule(ctx, appPool, rule)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget", "quantity": float64(1)})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if _, present := state.Payload["shouldNeverBeSet"]; present {
		t.Errorf("expected a non-autonomous rule's action to NOT run automatically, got payload %v", state.Payload)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		WHERE firm_id = $1 AND entity_type = 'workflow_rule' AND entity_id = $2 AND action = 'pending_approval'
	`, firmID, ruleID).Scan(&count); err != nil {
		t.Fatalf("count pending_approval audit entries: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 pending_approval audit_log entry, got %d", count)
	}
}

func TestListRulesForDefinition_ReturnsOnlyThatDefinitionsRules(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules E') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed stock_to_sale: %v", err)
	}
	if _, err := workflow.SeedCustomerPipelineWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed customer_pipeline: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-rules-list")

	stockRule := workflow.Rule{
		FirmID: firmID, DefinitionKey: workflow.StockToSaleKey, Name: "Stock rule",
		Trigger:       workflow.TriggerOnCreate,
		ConditionTree: workflow.ExpressionNode{Type: workflow.ConditionField, Field: "quantity", Op: string(workflow.OpGt), Value: float64(0)},
		Actions:       []workflow.Action{{Type: workflow.ActionNotify, Channel: workflow.NotifyChannelAuditLog, MessageTemplate: "x"}},
		Enabled:       true,
	}
	crmRule := stockRule
	crmRule.DefinitionKey = workflow.CustomerPipelineKey
	crmRule.Name = "CRM rule"

	if _, err := workflow.CreateRule(ctx, appPool, stockRule); err != nil {
		t.Fatalf("CreateRule (stock): %v", err)
	}
	if _, err := workflow.CreateRule(ctx, appPool, crmRule); err != nil {
		t.Fatalf("CreateRule (crm): %v", err)
	}

	rules, err := workflow.ListRulesForDefinition(ctx, appPool, firmID, userID, workflow.StockToSaleKey)
	if err != nil {
		t.Fatalf("ListRulesForDefinition: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected exactly 1 rule for stock_to_sale, got %d", len(rules))
	}
	if rules[0].Name != "Stock rule" {
		t.Errorf("expected rule name %q, got %q", "Stock rule", rules[0].Name)
	}
	if rules[0].ConditionTree.Field != "quantity" {
		t.Errorf("expected condition tree to round-trip, got %+v", rules[0].ConditionTree)
	}
	if len(rules[0].Actions) != 1 || rules[0].Actions[0].MessageTemplate != "x" {
		t.Errorf("expected actions to round-trip, got %+v", rules[0].Actions)
	}
}

// --- Rule Engine Builder UI backend: CreateRuleForFirm/UpdateRuleForFirm/
// DeleteRuleForFirm (owner-gated HTTP surface) ---------------------------

func validStockNotifyRule() workflow.Rule {
	return workflow.Rule{
		Name:    "Notify on quantity",
		Trigger: workflow.TriggerOnCreate,
		ConditionTree: workflow.ExpressionNode{
			Type: workflow.ConditionField, Field: "quantity", Op: string(workflow.OpGt), Value: float64(0),
		},
		Actions: []workflow.Action{
			{Type: workflow.ActionNotify, Channel: workflow.NotifyChannelAuditLog, MessageTemplate: "created {{item}}"},
		},
		Autonomous: true,
		Enabled:    true,
	}
}

func TestCreateRuleForFirm_OwnerCanCreateARule(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules F') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed stock_to_sale: %v", err)
	}
	ownerID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "sub-create-rule-owner")

	created, err := workflow.CreateRuleForFirm(ctx, appPool, firmID, ownerID, workflow.StockToSaleKey, validStockNotifyRule())
	if err != nil {
		t.Fatalf("CreateRuleForFirm: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("expected a non-nil rule ID")
	}
	if created.FirmID != firmID || created.DefinitionKey != workflow.StockToSaleKey {
		t.Errorf("expected firmID/definitionKey to be set from the call, got %+v", created)
	}

	rules, err := workflow.ListRulesForDefinition(ctx, appPool, firmID, ownerID, workflow.StockToSaleKey)
	if err != nil {
		t.Fatalf("ListRulesForDefinition: %v", err)
	}
	if len(rules) != 1 || rules[0].ID != created.ID {
		t.Fatalf("expected the created rule to be listed, got %+v", rules)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log WHERE firm_id = $1 AND entity_type = 'workflow_rule' AND entity_id = $2 AND action = 'create'
	`, firmID, created.ID).Scan(&count); err != nil {
		t.Fatalf("count create audit entries: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 create audit_log entry, got %d", count)
	}
}

func TestCreateRuleForFirm_NonOwnerIsDenied(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules G') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed stock_to_sale: %v", err)
	}
	memberID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-create-rule-nonowner")

	if _, err := workflow.CreateRuleForFirm(ctx, appPool, firmID, memberID, workflow.StockToSaleKey, validStockNotifyRule()); !errors.Is(err, workflow.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for a non-owner member, got: %v", err)
	}
}

func TestCreateRuleForFirm_NonMemberGetsNotFound(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA, firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules H-A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules H-B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmA); err != nil {
		t.Fatalf("seed stock_to_sale for firm A: %v", err)
	}
	outsiderID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmB, "sub-create-rule-outsider")

	if _, err := workflow.CreateRuleForFirm(ctx, appPool, firmA, outsiderID, workflow.StockToSaleKey, validStockNotifyRule()); !errors.Is(err, workflow.ErrDefinitionNotFound) {
		t.Fatalf("expected ErrDefinitionNotFound for an owner of a different firm supplying firm A's real ID, got: %v", err)
	}
}

func TestCreateRuleForFirm_UnknownDefinitionKeyIsRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules I') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "sub-create-rule-unknown-def")

	if _, err := workflow.CreateRuleForFirm(ctx, appPool, firmID, ownerID, "does_not_exist", validStockNotifyRule()); !errors.Is(err, workflow.ErrDefinitionNotFound) {
		t.Fatalf("expected ErrDefinitionNotFound for an unknown definition key, got: %v", err)
	}
}

func TestCreateRuleForFirm_RejectsMalformedConditionTree(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules J') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed stock_to_sale: %v", err)
	}
	ownerID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "sub-create-rule-malformed")

	rule := validStockNotifyRule()
	rule.ConditionTree = workflow.ExpressionNode{Op: string(workflow.LogicAnd)} // no children
	if _, err := workflow.CreateRuleForFirm(ctx, appPool, firmID, ownerID, workflow.StockToSaleKey, rule); !errors.Is(err, workflow.ErrInvalidRule) {
		t.Fatalf("expected ErrInvalidRule for a malformed condition tree, got: %v", err)
	}

	rule2 := validStockNotifyRule()
	rule2.Actions = []workflow.Action{{Type: "bogus"}}
	if _, err := workflow.CreateRuleForFirm(ctx, appPool, firmID, ownerID, workflow.StockToSaleKey, rule2); !errors.Is(err, workflow.ErrInvalidRule) {
		t.Fatalf("expected ErrInvalidRule for an unknown action type, got: %v", err)
	}
}

// TestCreateRuleForFirm_RejectsObviousSelfReferentialLoop covers the
// design brief's explicit ask: a rule triggered on_transition whose own
// action is a transition that is a literal self-loop (from == to state)
// must be rejected at creation time, not left to recurse at runtime.
func TestCreateRuleForFirm_RejectsObviousSelfReferentialLoop(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules K') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "sub-create-rule-selfloop")

	// A workflow definition with a genuine self-loop transition: "ping"
	// stays in "active" from "active".
	selfLoopSpec := workflow.DefinitionSpec{
		Key:  "self_loop_test",
		Name: "Self Loop Test",
		CreatePermission: workflow.PermissionSpec{
			Key: "workflow.self_loop_test.create", Description: "Create.",
		},
		States: []workflow.StateSpec{
			{Key: "active", Name: "Active", IsInitial: true},
		},
		Transitions: []workflow.TransitionSpec{
			{
				FromStateKey: "active", ToStateKey: "active", ActionKey: "ping", Name: "Ping",
				Permission: workflow.PermissionSpec{Key: "workflow.self_loop_test.ping", Description: "Ping."},
			},
		},
	}
	if _, err := workflow.DefineWorkflow(ctx, appPool, firmID, selfLoopSpec); err != nil {
		t.Fatalf("DefineWorkflow (self-loop spec): %v", err)
	}

	rule := workflow.Rule{
		Name:          "Self-referential",
		Trigger:       workflow.TriggerOnTransition,
		ConditionTree: workflow.ExpressionNode{Type: workflow.ConditionField, Field: "x", Op: string(workflow.OpEq), Value: "y"},
		Actions:       []workflow.Action{{Type: workflow.ActionTransition, ActionKey: "ping"}},
		Autonomous:    true,
		Enabled:       true,
	}
	if _, err := workflow.CreateRuleForFirm(ctx, appPool, firmID, ownerID, "self_loop_test", rule); !errors.Is(err, workflow.ErrInvalidRule) {
		t.Fatalf("expected ErrInvalidRule for an obvious self-referential loop, got: %v", err)
	}
}

func TestUpdateRuleForFirm_OwnerCanUpdate(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules L') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed stock_to_sale: %v", err)
	}
	ownerID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "sub-update-rule-owner")

	created, err := workflow.CreateRuleForFirm(ctx, appPool, firmID, ownerID, workflow.StockToSaleKey, validStockNotifyRule())
	if err != nil {
		t.Fatalf("CreateRuleForFirm: %v", err)
	}

	newName := "Renamed rule"
	disabled := false
	updated, err := workflow.UpdateRuleForFirm(ctx, appPool, firmID, ownerID, created.ID, workflow.StockToSaleKey, workflow.RuleUpdate{
		Name: &newName, Enabled: &disabled,
	})
	if err != nil {
		t.Fatalf("UpdateRuleForFirm: %v", err)
	}
	if updated.Name != newName {
		t.Errorf("expected name %q, got %q", newName, updated.Name)
	}
	if updated.Enabled {
		t.Error("expected the rule to now be disabled")
	}
	// Untouched fields survive the partial update.
	if updated.Actions[0].MessageTemplate != "created {{item}}" {
		t.Errorf("expected actions to survive an update that didn't touch them, got %+v", updated.Actions)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log WHERE firm_id = $1 AND entity_type = 'workflow_rule' AND entity_id = $2 AND action = 'update'
	`, firmID, created.ID).Scan(&count); err != nil {
		t.Fatalf("count update audit entries: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 update audit_log entry, got %d", count)
	}
}

func TestUpdateRuleForFirm_NonOwnerIsDenied(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules M') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed stock_to_sale: %v", err)
	}
	ownerID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "sub-update-rule-owner-2")
	memberID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-update-rule-nonowner")

	created, err := workflow.CreateRuleForFirm(ctx, appPool, firmID, ownerID, workflow.StockToSaleKey, validStockNotifyRule())
	if err != nil {
		t.Fatalf("CreateRuleForFirm: %v", err)
	}

	newName := "Should not apply"
	if _, err := workflow.UpdateRuleForFirm(ctx, appPool, firmID, memberID, created.ID, workflow.StockToSaleKey, workflow.RuleUpdate{Name: &newName}); !errors.Is(err, workflow.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for a non-owner member, got: %v", err)
	}
}

func TestUpdateRuleForFirm_CrossFirmRuleIDGetsNotFound(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA, firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules N-A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules N-B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmA); err != nil {
		t.Fatalf("seed stock_to_sale for firm A: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmB); err != nil {
		t.Fatalf("seed stock_to_sale for firm B: %v", err)
	}
	ownerA, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmA, "sub-update-rule-crossfirm-a")
	ownerB, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmB, "sub-update-rule-crossfirm-b")

	created, err := workflow.CreateRuleForFirm(ctx, appPool, firmA, ownerA, workflow.StockToSaleKey, validStockNotifyRule())
	if err != nil {
		t.Fatalf("CreateRuleForFirm: %v", err)
	}

	newName := "Should not apply"
	if _, err := workflow.UpdateRuleForFirm(ctx, appPool, firmB, ownerB, created.ID, workflow.StockToSaleKey, workflow.RuleUpdate{Name: &newName}); !errors.Is(err, workflow.ErrRuleNotFound) {
		t.Fatalf("expected ErrRuleNotFound for firm B's owner supplying firm A's real rule ID, got: %v", err)
	}
}

func TestDeleteRuleForFirm_OwnerCanDelete(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules O') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed stock_to_sale: %v", err)
	}
	ownerID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "sub-delete-rule-owner")

	created, err := workflow.CreateRuleForFirm(ctx, appPool, firmID, ownerID, workflow.StockToSaleKey, validStockNotifyRule())
	if err != nil {
		t.Fatalf("CreateRuleForFirm: %v", err)
	}

	if err := workflow.DeleteRuleForFirm(ctx, appPool, firmID, ownerID, created.ID, workflow.StockToSaleKey); err != nil {
		t.Fatalf("DeleteRuleForFirm: %v", err)
	}

	rules, err := workflow.ListRulesForDefinition(ctx, appPool, firmID, ownerID, workflow.StockToSaleKey)
	if err != nil {
		t.Fatalf("ListRulesForDefinition: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules after delete, got %d", len(rules))
	}

	var count int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log WHERE firm_id = $1 AND entity_type = 'workflow_rule' AND entity_id = $2 AND action = 'delete'
	`, firmID, created.ID).Scan(&count); err != nil {
		t.Fatalf("count delete audit entries: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 delete audit_log entry, got %d", count)
	}

	if err := workflow.DeleteRuleForFirm(ctx, appPool, firmID, ownerID, created.ID, workflow.StockToSaleKey); !errors.Is(err, workflow.ErrRuleNotFound) {
		t.Fatalf("expected ErrRuleNotFound deleting an already-deleted rule, got: %v", err)
	}
}

func TestDeleteRuleForFirm_NonOwnerIsDenied(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Rules P') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed stock_to_sale: %v", err)
	}
	ownerID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "sub-delete-rule-owner-2")
	memberID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-delete-rule-nonowner")

	created, err := workflow.CreateRuleForFirm(ctx, appPool, firmID, ownerID, workflow.StockToSaleKey, validStockNotifyRule())
	if err != nil {
		t.Fatalf("CreateRuleForFirm: %v", err)
	}

	if err := workflow.DeleteRuleForFirm(ctx, appPool, firmID, memberID, created.ID, workflow.StockToSaleKey); !errors.Is(err, workflow.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for a non-owner member, got: %v", err)
	}

	// The rule should still exist - a denied delete must not have any effect.
	rules, err := workflow.ListRulesForDefinition(ctx, appPool, firmID, ownerID, workflow.StockToSaleKey)
	if err != nil {
		t.Fatalf("ListRulesForDefinition: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected the rule to still exist after a denied delete, got %d rules", len(rules))
	}
}
