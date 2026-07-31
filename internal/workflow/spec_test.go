// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

func validStockToSaleSpec() workflow.DefinitionSpec {
	// A deliberate copy, not the package var itself, so tests can mutate
	// their own copy without affecting other tests or the real spec.
	spec := workflow.StockToSaleSpec
	spec.States = append([]workflow.StateSpec(nil), spec.States...)
	spec.Transitions = append([]workflow.TransitionSpec(nil), spec.Transitions...)
	return spec
}

func TestValidate_AcceptsStockToSaleSpec(t *testing.T) {
	if err := validStockToSaleSpec().Validate(); err != nil {
		t.Fatalf("expected the real Stock In -> Sale spec to validate, got: %v", err)
	}
}

// TestValidate_AcceptsCustomerPipelineSpec covers this batch's second
// default workflow (customer_pipeline.go). In particular, its two
// mark_lost transitions (lead->lost and qualified->lost) share both an
// action key and a permission key but differ in from-state - proving
// Validate's duplicate-transition check keys on (from-state, action),
// not action alone.
func TestValidate_AcceptsCustomerPipelineSpec(t *testing.T) {
	if err := workflow.CustomerPipelineSpec.Validate(); err != nil {
		t.Fatalf("expected the real Customer Pipeline spec to validate, got: %v", err)
	}
}

func TestCustomerPipelineSpec_PermissionKeysIncludesEveryTransitionOnce(t *testing.T) {
	keys := workflow.CustomerPipelineSpec.PermissionKeys()
	// CreatePermission + 4 transitions (qualify, convert, mark_lost x2) =
	// 5 entries, even though mark_lost's permission key repeats.
	if len(keys) != 5 {
		t.Fatalf("expected 5 permission key entries (1 create + 4 transitions), got %d: %v", len(keys), keys)
	}
}

func TestValidate_RejectsMissingKey(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Key = ""
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for empty definition key, got nil")
	}
}

func TestValidate_RejectsMissingCreatePermission(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.CreatePermission.Key = ""
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for empty create permission key, got nil")
	}
}

func TestValidate_RejectsNoStates(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.States = nil
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for zero states, got nil")
	}
}

func TestValidate_RejectsNoInitialState(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.States = []workflow.StateSpec{
		{Key: "in_stock", Name: "In Stock"},
		{Key: "sold", Name: "Sold", IsTerminal: true},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for zero initial states, got nil")
	}
}

func TestValidate_RejectsTwoInitialStates(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.States = []workflow.StateSpec{
		{Key: "in_stock", Name: "In Stock", IsInitial: true},
		{Key: "sold", Name: "Sold", IsInitial: true},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for two initial states, got nil")
	}
}

func TestValidate_RejectsDuplicateStateKeys(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.States = []workflow.StateSpec{
		{Key: "in_stock", Name: "In Stock", IsInitial: true},
		{Key: "in_stock", Name: "Duplicate"},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for duplicate state keys, got nil")
	}
}

func TestValidate_RejectsTransitionToUnknownState(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Transitions[0].ToStateKey = "no_such_state"
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for transition referencing an unknown to-state, got nil")
	}
}

func TestValidate_RejectsTransitionFromUnknownState(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Transitions[0].FromStateKey = "no_such_state"
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for transition referencing an unknown from-state, got nil")
	}
}

func TestValidate_RejectsTransitionMissingPermission(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Transitions[0].Permission.Key = ""
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for transition with no permission key, got nil")
	}
}

func TestValidate_RejectsDuplicateTransitionActionFromSameState(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.States = []workflow.StateSpec{
		{Key: "in_stock", Name: "In Stock", IsInitial: true},
		{Key: "sold", Name: "Sold", IsTerminal: true},
		{Key: "returned", Name: "Returned", IsTerminal: true},
	}
	spec.Transitions = []workflow.TransitionSpec{
		{FromStateKey: "in_stock", ToStateKey: "sold", ActionKey: "record_sale", Name: "Record Sale", Permission: workflow.PermissionSpec{Key: "workflow.stock_to_sale.record_sale", Description: "x"}},
		{FromStateKey: "in_stock", ToStateKey: "returned", ActionKey: "record_sale", Name: "Record Return", Permission: workflow.PermissionSpec{Key: "workflow.stock_to_sale.record_return", Description: "x"}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for two transitions sharing (from-state, action key), got nil")
	}
}

func TestValidate_AllowsSameActionKeyFromDifferentStates(t *testing.T) {
	// A 7-step manufacturing workflow will often reuse an action key like
	// "advance" from multiple stations - only (from-state, action) needs
	// to be unique, not the action key alone.
	spec := workflow.DefinitionSpec{
		Key:  "multi_step",
		Name: "Multi Step",
		CreatePermission: workflow.PermissionSpec{
			Key:         "workflow.multi_step.create",
			Description: "x",
		},
		States: []workflow.StateSpec{
			{Key: "step1", Name: "Step 1", IsInitial: true},
			{Key: "step2", Name: "Step 2"},
			{Key: "step3", Name: "Step 3", IsTerminal: true},
		},
		Transitions: []workflow.TransitionSpec{
			{FromStateKey: "step1", ToStateKey: "step2", ActionKey: "advance", Name: "Advance", Permission: workflow.PermissionSpec{Key: "workflow.multi_step.advance", Description: "x"}},
			{FromStateKey: "step2", ToStateKey: "step3", ActionKey: "advance", Name: "Advance", Permission: workflow.PermissionSpec{Key: "workflow.multi_step.advance", Description: "x"}},
		},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("expected reusing an action key from different from-states to validate, got: %v", err)
	}
}
