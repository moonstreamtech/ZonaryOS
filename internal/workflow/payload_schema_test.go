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

// testDefinitionSpec's payload schema for these tests: a required string
// (item), a required number (quantity), and an optional boolean (rush) -
// one field per FieldType this batch cares about proving, minus date
// (covered by TestCreateInstance_ValidatesDateField below on its own,
// smaller spec, to keep this one's happy-path payload simple).
//
// This is a NEW, test-only definition rather than a retrofit of
// StockToSaleSpec/CustomerPipelineSpec - see stock_to_sale.go and
// customer_pipeline.go's own doc comments, and the batch's final report,
// for why: several of this package's own existing tests create
// stock_to_sale/customer_pipeline instances with only a subset of their
// "real" fields (e.g. CreateInstance with just {"item": "widget"}, no
// quantity) - retrofitting either spec with a required field would break
// those tests, which is exactly the regression this batch is required not
// to cause. A schema-less definition (the existing specs, unmodified)
// keeps working exactly as before; this definition proves the schema
// mechanism itself end-to-end, the same role scripts/e2e_smoke_test.sh's
// schema-less "purchase_order" definition already plays for freeform
// behavior.
var testSchemaSpec = workflow.DefinitionSpec{
	Key:  "payload_schema_test",
	Name: "Payload Schema Test",
	CreatePermission: workflow.PermissionSpec{
		Key:         "workflow.payload_schema_test.create",
		Description: "Create a payload_schema_test instance.",
	},
	States: []workflow.StateSpec{
		{Key: "open", Name: "Open", IsInitial: true},
	},
	Fields: []workflow.FieldSpec{
		{Name: "item", Type: workflow.FieldTypeString, Required: true},
		{Name: "quantity", Type: workflow.FieldTypeNumber, Required: true},
		{Name: "rush", Type: workflow.FieldTypeBoolean, Required: false},
	},
}

func TestCreateInstance_SchemaRejectsMissingRequiredField(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.DefineWorkflow(ctx, appPool, firmID, testSchemaSpec)
	if err != nil {
		t.Fatalf("DefineWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-schema-missing", testSchemaSpec.CreatePermission.Key)

	// "item" is present but "quantity" (required) is missing entirely.
	_, err = workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget"})
	if !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for a missing required field, got: %v", err)
	}
}

func TestCreateInstance_SchemaRejectsWrongType(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.DefineWorkflow(ctx, appPool, firmID, testSchemaSpec)
	if err != nil {
		t.Fatalf("DefineWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-schema-wrongtype", testSchemaSpec.CreatePermission.Key)

	// "quantity" is declared FieldTypeNumber but a string is submitted.
	_, err = workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"item":     "widget",
		"quantity": "ten",
	})
	if !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for a wrong-typed field, got: %v", err)
	}
}

func TestCreateInstance_SchemaAcceptsValidPayload(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.DefineWorkflow(ctx, appPool, firmID, testSchemaSpec)
	if err != nil {
		t.Fatalf("DefineWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-schema-valid", testSchemaSpec.CreatePermission.Key)

	// A fully-valid payload, including the optional "rush" boolean - must
	// succeed and store every field, exactly like a schema-less
	// CreateInstance call always has.
	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"item":     "widget",
		"quantity": float64(10),
		"rush":     true,
	})
	if err != nil {
		t.Fatalf("CreateInstance with a fully-valid payload should succeed, got: %v", err)
	}

	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.Payload["item"] != "widget" || state.Payload["quantity"] != float64(10) || state.Payload["rush"] != true {
		t.Errorf("expected the full valid payload to be stored, got %+v", state.Payload)
	}

	// Omitting the OPTIONAL "rush" field must still succeed - only
	// required fields are enforced.
	if _, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{
		"item":     "gadget",
		"quantity": float64(1),
	}); err != nil {
		t.Errorf("expected CreateInstance to succeed when an optional field is simply omitted, got: %v", err)
	}
}

// TestCreateInstance_SchemaValidatesDateField covers FieldTypeDate on its
// own smaller spec (a required date field) - kept separate from the
// item/quantity/rush spec above so that spec's tests stay focused on
// string/number/boolean.
func TestCreateInstance_SchemaValidatesDateField(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	dateSpec := workflow.DefinitionSpec{
		Key:  "payload_schema_date_test",
		Name: "Payload Schema Date Test",
		CreatePermission: workflow.PermissionSpec{
			Key:         "workflow.payload_schema_date_test.create",
			Description: "Create a payload_schema_date_test instance.",
		},
		States: []workflow.StateSpec{
			{Key: "open", Name: "Open", IsInitial: true},
		},
		Fields: []workflow.FieldSpec{
			{Name: "dueDate", Type: workflow.FieldTypeDate, Required: true},
		},
	}

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.DefineWorkflow(ctx, appPool, firmID, dateSpec)
	if err != nil {
		t.Fatalf("DefineWorkflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-schema-date", dateSpec.CreatePermission.Key)

	if _, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"dueDate": "not-a-date"}); !errors.Is(err, workflow.ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for a malformed date, got: %v", err)
	}

	if _, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"dueDate": "2026-08-01"}); err != nil {
		t.Fatalf("expected a valid YYYY-MM-DD date to be accepted, got: %v", err)
	}
}

