// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"context"
	"encoding/json"
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
