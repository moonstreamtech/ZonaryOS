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

// Open Points item 38's runtime proof, mirroring payload_schema_test.go's
// own testSchemaSpec/style exactly (a NEW, test-only definition rather
// than a retrofit of StockToSaleSpec/CustomerPipelineSpec - see this
// batch's final report and stock_to_sale.go/customer_pipeline.go's own
// doc comments for why: this package's existing tests create
// stock_to_sale instances with only {"item": ...} and customer_pipeline
// instances with only {"name": ...}, so adding a new required field to
// either spec would break those tests, exactly the regression this batch
// must not cause).

// leadSourceSpec is the "reference target" definition these tests point
// their reference fields at - a minimal schema-less definition, playing
// the same role stock_to_sale/customer_pipeline already play for other
// tests: something with real instances to reference.
var leadSourceSpec = workflow.DefinitionSpec{
	Key:  "item38_lead_source",
	Name: "Item 38 Lead Source",
	CreatePermission: workflow.PermissionSpec{
		Key:         "workflow.item38_lead_source.create",
		Description: "Create an item38_lead_source instance.",
	},
	States: []workflow.StateSpec{
		{Key: "open", Name: "Open", IsInitial: true},
	},
}

// item38Spec exercises enum/reference/array in one definition - one field
// per new FieldType this batch adds, the same "one field per type in one
// spec" style testSchemaSpec (payload_schema_test.go) already uses for
// the original four.
var item38Spec = workflow.DefinitionSpec{
	Key:  "item38_payload_schema_test",
	Name: "Item 38 Payload Schema Test",
	CreatePermission: workflow.PermissionSpec{
		Key:         "workflow.item38_payload_schema_test.create",
		Description: "Create an item38_payload_schema_test instance.",
	},
	States: []workflow.StateSpec{
		{Key: "open", Name: "Open", IsInitial: true},
	},
	Fields: []workflow.FieldSpec{
		{Name: "priority", Type: workflow.FieldTypeEnum, Required: true, Options: []string{"low", "medium", "high"}},
		{Name: "source", Type: workflow.FieldTypeReference, Required: false, ReferenceDefinitionKey: leadSourceSpec.Key},
		{Name: "tags", Type: workflow.FieldTypeArray, Required: false, ArrayItemType: string(workflow.FieldTypeString)},
	},
}

func TestCreateInstance_EnumAcceptsValidOption(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.DefineWorkflow(ctx, appPool, firmID, leadSourceSpec); err != nil {
		t.Fatalf("DefineWorkflow(leadSourceSpec): %v", err)
	}
	definitionID, err := workflow.DefineWorkflow(ctx, appPool, firmID, item38Spec)
	if err != nil {
		t.Fatalf("DefineWorkflow(item38Spec): %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-enum-valid", item38Spec.CreatePermission.Key)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"priority": "high"})
	if err != nil {
		t.Fatalf("expected a valid enum value to be accepted, got: %v", err)
	}
	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.Payload["priority"] != "high" {
		t.Errorf("expected priority=high to be stored, got %+v", state.Payload)
	}
}

func TestCreateInstance_EnumRejectsInvalidOption(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.DefineWorkflow(ctx, appPool, firmID, leadSourceSpec); err != nil {
		t.Fatalf("DefineWorkflow(leadSourceSpec): %v", err)
	}
	definitionID, err := workflow.DefineWorkflow(ctx, appPool, firmID, item38Spec)
	if err != nil {
		t.Fatalf("DefineWorkflow(item38Spec): %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-enum-invalid", item38Spec.CreatePermission.Key)

	_, err = workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"priority": "urgent"})
	if !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for an out-of-set enum value, got: %v", err)
	}
}

func TestCreateInstance_ArrayAcceptsValidItems(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.DefineWorkflow(ctx, appPool, firmID, leadSourceSpec); err != nil {
		t.Fatalf("DefineWorkflow(leadSourceSpec): %v", err)
	}
	definitionID, err := workflow.DefineWorkflow(ctx, appPool, firmID, item38Spec)
	if err != nil {
		t.Fatalf("DefineWorkflow(item38Spec): %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-array-valid", item38Spec.CreatePermission.Key)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"priority": "low",
		"tags":     []any{"urgent", "vip"},
	})
	if err != nil {
		t.Fatalf("expected a valid string array to be accepted, got: %v", err)
	}
	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	tags, ok := state.Payload["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "urgent" || tags[1] != "vip" {
		t.Errorf("expected tags=[urgent vip] to round-trip, got %+v", state.Payload["tags"])
	}
}

func TestCreateInstance_ArrayRejectsWrongItemType(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.DefineWorkflow(ctx, appPool, firmID, leadSourceSpec); err != nil {
		t.Fatalf("DefineWorkflow(leadSourceSpec): %v", err)
	}
	definitionID, err := workflow.DefineWorkflow(ctx, appPool, firmID, item38Spec)
	if err != nil {
		t.Fatalf("DefineWorkflow(item38Spec): %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-array-invalid", item38Spec.CreatePermission.Key)

	// "tags" is declared arrayItemType=string, but one element is a bool.
	_, err = workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"priority": "low",
		"tags":     []any{"urgent", true},
	})
	if !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for a wrong-typed array element, got: %v", err)
	}
}

func TestCreateInstance_ArrayRejectsNonArrayValue(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.DefineWorkflow(ctx, appPool, firmID, leadSourceSpec); err != nil {
		t.Fatalf("DefineWorkflow(leadSourceSpec): %v", err)
	}
	definitionID, err := workflow.DefineWorkflow(ctx, appPool, firmID, item38Spec)
	if err != nil {
		t.Fatalf("DefineWorkflow(item38Spec): %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-array-notarray", item38Spec.CreatePermission.Key)

	_, err = workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"priority": "low",
		"tags":     "not-an-array",
	})
	if !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation when \"tags\" isn't a JSON array at all, got: %v", err)
	}
}

func TestCreateInstance_ReferenceAcceptsValidInstance(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	sourceDefID, err := workflow.DefineWorkflow(ctx, appPool, firmID, leadSourceSpec)
	if err != nil {
		t.Fatalf("DefineWorkflow(leadSourceSpec): %v", err)
	}
	definitionID, err := workflow.DefineWorkflow(ctx, appPool, firmID, item38Spec)
	if err != nil {
		t.Fatalf("DefineWorkflow(item38Spec): %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID,
		"sub-reference-valid", item38Spec.CreatePermission.Key, leadSourceSpec.CreatePermission.Key)

	sourceInstanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, sourceDefID, map[string]any{})
	if err != nil {
		t.Fatalf("CreateInstance(leadSourceSpec): %v", err)
	}

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"priority": "low",
		"source":   sourceInstanceID.String(),
	})
	if err != nil {
		t.Fatalf("expected a reference to a real, same-firm instance to be accepted, got: %v", err)
	}
	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.Payload["source"] != sourceInstanceID.String() {
		t.Errorf("expected source=%s to be stored, got %+v", sourceInstanceID, state.Payload["source"])
	}
}

func TestCreateInstance_ReferenceRejectsNonexistentInstance(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.DefineWorkflow(ctx, appPool, firmID, leadSourceSpec); err != nil {
		t.Fatalf("DefineWorkflow(leadSourceSpec): %v", err)
	}
	definitionID, err := workflow.DefineWorkflow(ctx, appPool, firmID, item38Spec)
	if err != nil {
		t.Fatalf("DefineWorkflow(item38Spec): %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-reference-nonexistent", item38Spec.CreatePermission.Key)

	_, err = workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"priority": "low",
		"source":   uuid.New().String(),
	})
	if !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for a reference to a nonexistent instance ID, got: %v", err)
	}
}

func TestCreateInstance_ReferenceRejectsMalformedID(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.DefineWorkflow(ctx, appPool, firmID, leadSourceSpec); err != nil {
		t.Fatalf("DefineWorkflow(leadSourceSpec): %v", err)
	}
	definitionID, err := workflow.DefineWorkflow(ctx, appPool, firmID, item38Spec)
	if err != nil {
		t.Fatalf("DefineWorkflow(item38Spec): %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-reference-malformed", item38Spec.CreatePermission.Key)

	_, err = workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"priority": "low",
		"source":   "not-a-uuid",
	})
	if !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for a malformed (non-UUID) reference value, got: %v", err)
	}
}

// TestCreateInstance_ReferenceRejectsCrossFirmInstance is this batch's
// explicit isolation check: a reference field's value must be an instance
// ID that exists in the SAME firm as the instance being created, not just
// anywhere in the database. Two firms each define leadSourceSpec/
// item38Spec independently (workflow_definitions.key is scoped per firm,
// not globally unique - see migrations/0003_workflow_engine.up.sql's
// (firm_id, key) constraint), firm A creates a real leadSourceSpec
// instance, and firm B's CreateInstance call tries to reference firm A's
// instance ID by value - it must be rejected exactly like a nonexistent
// ID would be (checkReferenceField's WHERE clause filters on firmID
// explicitly, on top of the RLS session variable WithFirmContext already
// sets - see engine.go's doc comment on checkReferenceField for the
// "defense in depth" reasoning for both).
func TestCreateInstance_ReferenceRejectsCrossFirmInstance(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	var firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}

	sourceDefA, err := workflow.DefineWorkflow(ctx, appPool, firmA, leadSourceSpec)
	if err != nil {
		t.Fatalf("DefineWorkflow(leadSourceSpec) for firm A: %v", err)
	}
	if _, err := workflow.DefineWorkflow(ctx, appPool, firmA, item38Spec); err != nil {
		t.Fatalf("DefineWorkflow(item38Spec) for firm A: %v", err)
	}
	userA, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmA, "sub-crossfirm-a", leadSourceSpec.CreatePermission.Key)

	sourceInstanceA, err := workflow.CreateInstance(ctx, appPool, firmA, userA, sourceDefA, map[string]any{})
	if err != nil {
		t.Fatalf("CreateInstance(leadSourceSpec) for firm A: %v", err)
	}

	if _, err := workflow.DefineWorkflow(ctx, appPool, firmB, leadSourceSpec); err != nil {
		t.Fatalf("DefineWorkflow(leadSourceSpec) for firm B: %v", err)
	}
	definitionB, err := workflow.DefineWorkflow(ctx, appPool, firmB, item38Spec)
	if err != nil {
		t.Fatalf("DefineWorkflow(item38Spec) for firm B: %v", err)
	}
	userB, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmB, "sub-crossfirm-b", item38Spec.CreatePermission.Key)

	// firm B's CreateInstance references firm A's real instance ID -
	// must be rejected exactly like a nonexistent ID (same error, same
	// no-metadata-leak reasoning as ErrDefinitionNotFound elsewhere in
	// this package: a caller can't distinguish "doesn't exist" from
	// "exists in a firm you can't see" from the response).
	_, err = workflow.CreateInstance(ctx, appPool, firmB, userB, definitionB, map[string]any{
		"priority": "low",
		"source":   sourceInstanceA.String(),
	})
	if !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for a reference to another firm's instance, got: %v", err)
	}
}

// TestDefineWorkflowTx_RejectsReferenceToNonexistentDefinition covers the
// design decision this batch's report explains: Validate() alone cannot
// tell whether ReferenceDefinitionKey names a real definition (no DB/firm
// context - see spec_test.go's TestValidate_CannotCatchNonexistentReferenceTarget),
// so DefineWorkflowTx checks it instead, with the transaction/firmID it
// already has.
func TestDefineWorkflowTx_RejectsReferenceToNonexistentDefinition(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	spec := workflow.DefinitionSpec{
		Key:  "item38_dangling_reference",
		Name: "Item 38 Dangling Reference",
		CreatePermission: workflow.PermissionSpec{
			Key:         "workflow.item38_dangling_reference.create",
			Description: "x",
		},
		States: []workflow.StateSpec{
			{Key: "open", Name: "Open", IsInitial: true},
		},
		Fields: []workflow.FieldSpec{
			{Name: "source", Type: workflow.FieldTypeReference, ReferenceDefinitionKey: "no_such_definition_key"},
		},
	}

	_, err := workflow.DefineWorkflow(ctx, appPool, firmID, spec)
	if !errors.Is(err, workflow.ErrInvalidSpec) {
		t.Fatalf("expected ErrInvalidSpec when a reference field's target definition key doesn't exist for this firm, got: %v", err)
	}
}

// TestDefineWorkflowTx_AcceptsReferenceToRealDefinition is the accepting
// counterpart: once leadSourceSpec has actually been defined for the
// firm, a field referencing its key must be accepted.
func TestDefineWorkflowTx_AcceptsReferenceToRealDefinition(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.DefineWorkflow(ctx, appPool, firmID, leadSourceSpec); err != nil {
		t.Fatalf("DefineWorkflow(leadSourceSpec): %v", err)
	}
	if _, err := workflow.DefineWorkflow(ctx, appPool, firmID, item38Spec); err != nil {
		t.Fatalf("expected item38Spec (which references leadSourceSpec's real key) to define cleanly, got: %v", err)
	}
}
