// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CustomerPipelineKey is the workflow_definitions.key for this batch's
// second default workflow: a CRM lead pipeline. docs/VISION.md's "a
// business's relationship with its own customers is also part of the
// core" scope point (see §1/§2) named CRM as an in-scope area from the
// start; this is that area's first concrete, seeded example, built the
// exact same way stock_to_sale.go's Stock In -> Sale is - a DefinitionSpec
// value, no new engine code. Every firm the wizard creates from here on
// gets both workflows seeded side by side (see
// internal/wizard.CreateDefaultFirm) - one inventory-shaped, one
// relationship-shaped - as a live demonstration that the generic engine
// isn't secretly tuned to one shape of business process.
const CustomerPipelineKey = "customer_pipeline"

// Permission keys the Customer Pipeline workflow registers into the
// global permissions catalog (see DefineWorkflow) - exported so callers
// and tests reference the same constants instead of duplicating string
// literals, mirroring stock_to_sale.go's own AddStockPermission/
// RecordSalePermission constants.
const (
	CreateLeadPermission      = "workflow.customer_pipeline.create_lead"
	QualifyLeadPermission     = "workflow.customer_pipeline.qualify"
	ConvertCustomerPermission = "workflow.customer_pipeline.convert"
	MarkLeadLostPermission    = "workflow.customer_pipeline.mark_lost"
)

// markLostPermission is the shared PermissionSpec value both mark_lost
// transitions below reference - they're the same permission (one action
// a caller either can or can't do, "mark a lead lost"), just reachable
// from two different states, not two separate capabilities. upsertPermission's
// ON CONFLICT DO NOTHING makes registering it twice (once per transition)
// harmless.
var markLostPermission = PermissionSpec{
	Key:         MarkLeadLostPermission,
	Description: "Mark a lead or qualified lead as lost.",
}

// CustomerPipelineSpec is the concrete "Lead -> Qualified -> Customer"
// CRM pipeline: a lead is created, optionally qualified, then either
// converted into a customer or marked lost. mark_lost is deliberately
// reachable from both lead and qualified (not just one of them) - a lead
// can go cold before ever being qualified, or after - modeled as two
// TransitionSpec rows sharing the same action_key and permission, per
// DefinitionSpec's own "same (from-state, action) pair must be unique"
// rule (see spec.go's Validate): two different from-states, so no
// collision.
//
// Fields adds four OPTIONAL payload fields (Logistics/CRM batch,
// same "optional and additive" contract every other batch's own Fields
// addition follows) - name/email/phone/address, the customer-record
// fields the "convert" transition's CustomerTemplate below reads. None
// are Required=true: every ExecuteTransition/CreateInstance call that
// predates this batch (this package's own tests, the E2E smoke test's
// earlier customer_pipeline coverage - lead/qualify/convert with a
// completely freeform payload) keeps working unchanged (Rule 6).
// CreateInstanceForm's free-form key/value editor remains exactly as
// usable for any OTHER lead attribute a firm wants to track (item 35
// stays additive, not exhaustive - see FieldSpec's own doc comment).
var CustomerPipelineSpec = DefinitionSpec{
	Key:  CustomerPipelineKey,
	Name: "Customer Pipeline",
	CreatePermission: PermissionSpec{
		Key:         CreateLeadPermission,
		Description: "Add a new lead (starts a Customer Pipeline workflow instance).",
	},
	States: []StateSpec{
		{Key: "lead", Name: "Lead", IsInitial: true},
		{Key: "qualified", Name: "Qualified"},
		{Key: "customer", Name: "Customer", IsTerminal: true},
		{Key: "lost", Name: "Lost", IsTerminal: true},
	},
	Fields: []FieldSpec{
		{Name: "name", Type: FieldTypeString, Required: false},
		{Name: "email", Type: FieldTypeString, Required: false},
		{Name: "phone", Type: FieldTypeString, Required: false},
		{Name: "address", Type: FieldTypeString, Required: false},
	},
	Transitions: []TransitionSpec{
		{
			FromStateKey: "lead",
			ToStateKey:   "qualified",
			ActionKey:    "qualify",
			Name:         "Qualify",
			Permission: PermissionSpec{
				Key:         QualifyLeadPermission,
				Description: "Qualify a lead.",
			},
		},
		{
			FromStateKey: "qualified",
			ToStateKey:   "customer",
			ActionKey:    "convert",
			Name:         "Convert",
			Permission: PermissionSpec{
				Key:         ConvertCustomerPermission,
				Description: "Convert a qualified lead into a customer.",
			},
			// The workflow-to-CRM bridge (Logistics/CRM batch): "the
			// customer_pipeline workflow ... has no actual customer
			// record behind it. When a lead converts ... auto-create a
			// customer record" (design brief). When name is present on
			// this call, ExecuteTransition creates a customers row (with
			// SourceWorkflowInstance set to this instance - the
			// "active_customers" KPI's own condition, internal/reports/kpi.go)
			// in the SAME transaction as the state change - see
			// CustomerTemplate's own doc comment. Left unset on any call
			// that predates this batch (no name supplied) means "create
			// nothing" - this package's own pre-existing tests that
			// convert a lead with a freeform, name-less payload keep
			// working unchanged (Rule 6).
			Customer: &CustomerTemplate{
				NameField:    "name",
				EmailField:   "email",
				PhoneField:   "phone",
				AddressField: "address",
			},
		},
		{
			FromStateKey: "lead",
			ToStateKey:   "lost",
			ActionKey:    "mark_lost",
			Name:         "Mark Lost",
			Permission:   markLostPermission,
		},
		{
			FromStateKey: "qualified",
			ToStateKey:   "lost",
			ActionKey:    "mark_lost",
			Name:         "Mark Lost",
			Permission:   markLostPermission,
		},
	},
}

// SeedCustomerPipelineWorkflow instantiates CustomerPipelineSpec for
// firmID, returning its workflow_definitions.id. Intended for test
// fixtures - real firm provisioning goes through
// SeedCustomerPipelineWorkflowTx below, same split as
// SeedStockToSaleWorkflow/SeedStockToSaleWorkflowTx.
//
// ciaudit:ignore-firmid-check: thin wrapper around DefineWorkflow, same
// reasoning as SeedStockToSaleWorkflow - provisioning-only, never exposed
// via an HTTP handler.
func SeedCustomerPipelineWorkflow(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflow(ctx, pool, firmID, CustomerPipelineSpec)
}

// SeedCustomerPipelineWorkflowTx is SeedCustomerPipelineWorkflow's
// DefineWorkflowTx counterpart, for callers that need it as one step
// inside a larger transaction - namely the firm-creation wizard, which
// provisions this workflow atomically alongside the firm, its default
// role, and StockToSaleSpec. granteeRoleID is granted every permission
// this workflow introduces (DefineWorkflowTx's self-action auto-grant),
// same as SeedStockToSaleWorkflowTx.
//
// ciaudit:ignore-firmid-check: thin wrapper around DefineWorkflowTx, same
// reasoning as SeedStockToSaleWorkflowTx - only called by
// internal/wizard.CreateDefaultFirm with a firmID it just created, and
// test fixtures.
func SeedCustomerPipelineWorkflowTx(ctx context.Context, tx pgx.Tx, firmID, granteeRoleID uuid.UUID) (uuid.UUID, error) {
	return DefineWorkflowTx(ctx, tx, firmID, granteeRoleID, CustomerPipelineSpec)
}
