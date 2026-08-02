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

// TestValidate_AcceptsNoFieldSchema is Open Points item 35's direct
// regression proof at the spec.Validate level: a DefinitionSpec with a
// nil Fields - every DefinitionSpec that predates item 35, including the
// two real specs above - must keep validating with no new requirement
// introduced by the Fields addition.
func TestValidate_AcceptsNoFieldSchema(t *testing.T) {
	if err := validStockToSaleSpec().Validate(); err != nil {
		t.Fatalf("a schema-less spec should still validate cleanly, got: %v", err)
	}
}

// TestValidate_AcceptsWellFormedFieldSchema covers Validate's new Fields
// branch on the accepting side: one field per FieldType, a mix of
// required/optional, must not be rejected.
func TestValidate_AcceptsWellFormedFieldSchema(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "item", Type: workflow.FieldTypeString, Required: true},
		{Name: "quantity", Type: workflow.FieldTypeNumber, Required: true},
		{Name: "rush", Type: workflow.FieldTypeBoolean, Required: false},
		{Name: "dueDate", Type: workflow.FieldTypeDate, Required: false},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("expected a well-formed four-type field schema to validate, got: %v", err)
	}
}

// TestValidate_RejectsUnknownFieldType covers Validate's new Fields
// branch on the rejecting side: a FieldType outside the fixed four
// (string/number/boolean/date - see spec.go's own doc comment on why
// there are only four) must fail structurally, before ever reaching the
// database.
func TestValidate_RejectsUnknownFieldType(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "weird", Type: workflow.FieldType("object"), Required: false},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected Validate to reject an unknown FieldType, got nil error")
	}
}

// TestValidate_RejectsDuplicateFieldName covers Validate's new Fields
// branch's uniqueness check - two fields declaring the same Name is
// structurally ambiguous (which declaration governs?), same category of
// rejection as a duplicate state key.
func TestValidate_RejectsDuplicateFieldName(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "item", Type: workflow.FieldTypeString, Required: true},
		{Name: "item", Type: workflow.FieldTypeNumber, Required: false},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected Validate to reject a duplicate field name, got nil error")
	}
}

// TestValidate_RejectsEmptyFieldName covers Validate's new Fields
// branch's non-empty-name check, mirroring the existing empty-state-key
// rejection.
func TestValidate_RejectsEmptyFieldName(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "", Type: workflow.FieldTypeString, Required: true},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected Validate to reject an empty field name, got nil error")
	}
}

// Open Points item 38's structural Validate() coverage: enum/reference/
// array field shapes, mirroring the "one test per FieldType" style
// TestValidate_AcceptsWellFormedFieldSchema/TestValidate_RejectsUnknownFieldType
// already use for the original four types above.

func TestValidate_AcceptsWellFormedEnumField(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "priority", Type: workflow.FieldTypeEnum, Options: []string{"low", "medium", "high"}},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("expected a well-formed enum field to validate, got: %v", err)
	}
}

func TestValidate_RejectsEnumFieldWithNoOptions(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "priority", Type: workflow.FieldTypeEnum, Options: nil},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected Validate to reject an enum field with zero options, got nil error")
	}
}

func TestValidate_RejectsEnumFieldWithEmptyOption(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "priority", Type: workflow.FieldTypeEnum, Options: []string{"low", ""}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected Validate to reject an enum field with an empty-string option, got nil error")
	}
}

func TestValidate_RejectsEnumFieldWithDuplicateOption(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "priority", Type: workflow.FieldTypeEnum, Options: []string{"low", "low"}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected Validate to reject an enum field with a duplicate option, got nil error")
	}
}

func TestValidate_RejectsScalarFieldWithOptionsSet(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "item", Type: workflow.FieldTypeString, Options: []string{"widget"}},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected Validate to reject a string field carrying an enum-only Options value, got nil error")
	}
}

func TestValidate_AcceptsWellFormedReferenceField(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "relatedStock", Type: workflow.FieldTypeReference, ReferenceDefinitionKey: "stock_to_sale"},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("expected a well-formed reference field to validate, got: %v", err)
	}
}

func TestValidate_RejectsReferenceFieldWithEmptyKey(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "relatedStock", Type: workflow.FieldTypeReference, ReferenceDefinitionKey: ""},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected Validate to reject a reference field with an empty ReferenceDefinitionKey, got nil error")
	}
}

// TestValidate_CannotCatchNonexistentReferenceTarget documents the design
// boundary this batch's report explains at length: Validate is a pure,
// DB-free function with no firm context, so it structurally CANNOT tell
// whether "some_definition_that_does_not_exist" is real - that check only
// happens at DefineWorkflowTx (engine.go), which has a live transaction
// and firmID. This spec-level Validate call must therefore still accept a
// reference field naming a key nobody has defined - proving the gap
// Validate leaves is exactly where the doc comments say it is, not
// silently narrower or wider.
func TestValidate_CannotCatchNonexistentReferenceTarget(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "relatedThing", Type: workflow.FieldTypeReference, ReferenceDefinitionKey: "some_definition_that_does_not_exist"},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate should accept a structurally well-formed reference field regardless of whether the target key actually exists (that's DefineWorkflowTx's job) - got: %v", err)
	}
}

func TestValidate_AcceptsWellFormedArrayField(t *testing.T) {
	for _, itemType := range []workflow.FieldType{
		workflow.FieldTypeString, workflow.FieldTypeNumber, workflow.FieldTypeBoolean, workflow.FieldTypeDate,
	} {
		spec := validStockToSaleSpec()
		spec.Fields = []workflow.FieldSpec{
			{Name: "tags", Type: workflow.FieldTypeArray, ArrayItemType: string(itemType)},
		}
		if err := spec.Validate(); err != nil {
			t.Fatalf("expected an array field with item type %q to validate, got: %v", itemType, err)
		}
	}
}

// TestValidate_RejectsNestedArray and TestValidate_RejectsArrayOfReferences
// are this batch's explicit scope-boundary proof: "no nested arrays, no
// arrays of references" must be rejected at spec-definition time, not
// silently mishandled at CreateInstance/runtime time.
func TestValidate_RejectsNestedArray(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "matrix", Type: workflow.FieldTypeArray, ArrayItemType: string(workflow.FieldTypeArray)},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected Validate to reject a nested array (arrayItemType == \"array\"), got nil error")
	}
}

func TestValidate_RejectsArrayOfReferences(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "relatedThings", Type: workflow.FieldTypeArray, ArrayItemType: string(workflow.FieldTypeReference)},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected Validate to reject an array of references (arrayItemType == \"reference\"), got nil error")
	}
}

func TestValidate_RejectsArrayOfEnums(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "choices", Type: workflow.FieldTypeArray, ArrayItemType: string(workflow.FieldTypeEnum)},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected Validate to reject an array of enums (arrayItemType == \"enum\"), got nil error")
	}
}

func TestValidate_RejectsArrayFieldWithInvalidItemType(t *testing.T) {
	spec := validStockToSaleSpec()
	spec.Fields = []workflow.FieldSpec{
		{Name: "tags", Type: workflow.FieldTypeArray, ArrayItemType: "object"},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected Validate to reject an array field with an unrecognized ArrayItemType, got nil error")
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
