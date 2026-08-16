// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package workflow implements ZonaryOS's generic, graph-based workflow
// engine (Vision §3, "Unlimited Process Flexibility"): a business's process
// is a state machine defined by rows in the database - workflow_states,
// workflow_transitions - not by Go code written per workflow. This file
// defines the spec shape DefineWorkflow consumes; a 7-step manufacturing
// flow is just a bigger DefinitionSpec value, not a new code path.
package workflow

import (
	"encoding/json"
	"fmt"
)

// PermissionSpec is a permission catalog entry a workflow definition or
// transition requires. DefineWorkflow upserts these into the global
// `permissions` table (see migrations/0003_workflow_engine.up.sql) as part
// of defining the workflow, so a new workflow never needs a hand-written
// migration of its own just to register its permission keys.
type PermissionSpec struct {
	Key         string
	Description string
}

// StateSpec is one node in the workflow's state graph.
type StateSpec struct {
	Key        string
	Name       string
	IsInitial  bool
	IsTerminal bool
}

// TransitionSpec is one edge: moving from one state to another via a named
// action, gated by Permission (the Rule 7 tag for this transition).
//
// Side-effect templates: a transition can carry any number of the
// declarative, OPTIONAL "bridge" templates below, each attached as an
// entry in Effects - the ledger bridge (JournalTemplate), the inventory
// bridge (StockAdjustmentTemplate), the logistics bridge
// (DeliveryTemplate), the CRM bridge (CustomerTemplate), and the
// invoicing bridge (InvoiceTemplate). All five share one shape and one
// contract, deliberately: each is resolved by ExecuteTransition against
// the transition's merged instance payload at transition time (never
// baked in ahead of time), and applied in the SAME database transaction
// as the state change itself, through that module's own `*Tx` primitive
// (PostJournalEntryTx / AdjustStockTx / CreateDeliveryTx /
// CreateCustomerTx / CreateInvoiceTx) - so a side effect can never end up
// out of sync with the transition that produced it. A transition can
// carry several at once (e.g. stock_to_sale's record_sale carries
// Journal AND StockAdjustment AND Delivery AND Invoice effects) - they
// are independent, each resolved and applied on its own, not a single
// combined mechanism. A TransitionSpec with no Effects (every
// TransitionSpec that existed before the first bridge template was
// added) keeps behaving exactly as before - Rule 6.
//
// Effects (this refactor): a generic `[]TransitionEffect` slice, each
// entry a `{Kind, Payload}` pair - Payload is the exact same
// JournalTemplate/StockAdjustmentTemplate/DeliveryTemplate/
// CustomerTemplate/InvoiceTemplate struct every one of those five bridges
// already used, just marshalled into a Kind-tagged envelope instead of
// living behind its own named `*XTemplate` struct field. This replaces
// what used to be five separate named fields (Journal, StockAdjustment,
// Delivery, Customer, Invoice) - the fifth of which (Invoice) crossed
// this struct's own previously-documented threshold ("a fifth or sixth
// such bridge... is the signal to switch to the generic Effects shape").
// That refactor is this one. See the JournalEffect/StockEffect/
// DeliveryEffect/CustomerEffect/InvoiceEffect constructors below for how
// a DefinitionSpec author attaches one (stock_to_sale.go's record_sale is
// the concrete multi-effect example), and engine.go's extractEffect for
// how ExecuteTransition/fetchTransitionInfos read one back out - both
// keep the exact same per-kind Go types and per-kind HTTP JSON wire shape
// (`journal`/`stockAdjustment`/`delivery`/`customer`/`invoice` request and
// response fields, handlers.go) that existed before this refactor; only
// TransitionSpec's own internal shape and workflow_transitions' storage
// column changed - see migrations/0017_workflow_effects_refactor.up.sql's
// own doc comment for the stored-data migration, and this batch's report
// for the full backward-compatibility proof.
//
// A sixth genuinely distinct bridge kind is a new EffectKind constant, a
// new `*Template` struct, a new constructor, and a new case in
// extractEffect's callers - no further schema change, since Effects is
// already the open-ended shape this batch's threshold called for.
type TransitionSpec struct {
	FromStateKey string
	ToStateKey   string
	ActionKey    string
	Name         string
	Permission   PermissionSpec
	Effects      []TransitionEffect `json:"effects,omitempty"`
}

// TransitionEffect is one Kind-tagged side effect a transition carries -
// Payload is the corresponding template struct
// (JournalTemplate/StockAdjustmentTemplate/DeliveryTemplate/
// CustomerTemplate/InvoiceTemplate), marshalled - see this file's
// EffectKind constants for the closed set of valid Kind values and the
// JournalEffect/StockEffect/DeliveryEffect/CustomerEffect/InvoiceEffect
// constructors for the normal way to build one.
type TransitionEffect struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// EffectKind is TransitionEffect.Kind's closed vocabulary - one constant
// per bridge template this package defines. Deliberately plain strings
// (not a named EffectKind type) so TransitionEffect.Kind round-trips
// through JSON/jsonb storage with no custom marshalling.
const (
	EffectKindJournal  = "journal"
	EffectKindStock    = "stock"
	EffectKindDelivery = "delivery"
	EffectKindCustomer = "customer"
	EffectKindInvoice  = "invoice"
	// EffectKindSalesOrder/EffectKindPurchaseOrder (sales orders + full
	// procurement cycle batch) are the sixth and seventh bridge kinds -
	// added exactly the way this file's own "sixth bridge" doc comment
	// (TransitionSpec, above) already anticipated: a new EffectKind
	// constant, a new *Template struct, a new constructor, a new
	// validateEffect case, and a new extractEffect call site in
	// engine.go - no further schema change, since Effects (this file's
	// prior refactor, triggered by Invoice as the fifth bridge) is
	// already the open-ended shape this needed.
	EffectKindSalesOrder    = "salesOrder"
	EffectKindPurchaseOrder = "purchaseOrder"
)

// newEffect marshals payload (always one of this package's own template
// structs) into a Kind-tagged TransitionEffect - the shared primitive
// JournalEffect/StockEffect/DeliveryEffect/CustomerEffect/InvoiceEffect
// below all call. Every caller in this codebase passes a static Go
// literal built at package-init time (stock_to_sale.go/customer_pipeline.go/
// purchase_order.go's own DefinitionSpec vars) - a marshal failure here
// would mean one of those literals is itself unmarshalable, which never
// happens for the plain string/slice fields these template structs
// actually have, so a panic (surfacing at process startup, long before
// any HTTP request) is preferable to silently dropping an effect.
func newEffect(kind string, payload any) TransitionEffect {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("workflow: marshal %s effect: %v", kind, err))
	}
	return TransitionEffect{Kind: kind, Payload: json.RawMessage(data)}
}

// JournalEffect/StockEffect/DeliveryEffect/CustomerEffect/InvoiceEffect
// wrap the corresponding template struct into a TransitionEffect ready to
// append to TransitionSpec.Effects - see stock_to_sale.go's record_sale
// for the concrete multi-effect example.
func JournalEffect(jt JournalTemplate) TransitionEffect { return newEffect(EffectKindJournal, jt) }
func StockEffect(sat StockAdjustmentTemplate) TransitionEffect {
	return newEffect(EffectKindStock, sat)
}
func DeliveryEffect(dt DeliveryTemplate) TransitionEffect { return newEffect(EffectKindDelivery, dt) }
func CustomerEffect(ct CustomerTemplate) TransitionEffect { return newEffect(EffectKindCustomer, ct) }
func InvoiceEffect(it InvoiceTemplate) TransitionEffect   { return newEffect(EffectKindInvoice, it) }

// SalesOrderEffect/PurchaseOrderEffect (sixth/seventh bridges) wrap
// SalesOrderTemplate/PurchaseOrderTemplate into a TransitionEffect ready
// to append to TransitionSpec.Effects - see stock_to_sale.go's
// record_sale and purchase_order.go's send for the concrete examples.
func SalesOrderEffect(sot SalesOrderTemplate) TransitionEffect {
	return newEffect(EffectKindSalesOrder, sot)
}
func PurchaseOrderEffect(pot PurchaseOrderTemplate) TransitionEffect {
	return newEffect(EffectKindPurchaseOrder, pot)
}

// DeliveryTemplate describes the deliveries row a transition should
// create - the declarative shape a workflow definition carries; engine.go
// resolves it against one specific instance's payload at ExecuteTransition
// time, the same "resolved fresh per call, never baked in ahead of time"
// reasoning every other template in this file gives.
type DeliveryTemplate struct {
	// DestinationAddressField names the payload field holding the
	// delivery's destination address - e.g. "destination_address". The
	// whole template is skipped (not an error) if this field is absent
	// from the merged payload when the transition fires - the same
	// Rule-6-in-spirit reasoning StockAdjustmentTemplate.ProductField's
	// own doc comment gives: an existing/legit caller that never supplied
	// a destination address must keep working unchanged, and a phantom
	// delivery record with no destination is worse than no record at all.
	DestinationAddressField string
	// OriginAddressField/CarrierField/TrackingNumberField/ReferenceField
	// name the payload fields holding the corresponding deliveries
	// column - all OPTIONAL: an absent field simply leaves that column
	// NULL on the created row (unlike DestinationAddressField, a missing
	// one of these does NOT skip the whole template).
	OriginAddressField  string
	CarrierField        string
	TrackingNumberField string
	ReferenceField      string
}

// CustomerTemplate describes the customers row a transition should
// create - the declarative shape a workflow definition carries; engine.go
// resolves it against one specific instance's payload at ExecuteTransition
// time, the same "resolved fresh per call, never baked in ahead of time"
// reasoning every other template in this file gives.
type CustomerTemplate struct {
	// NameField names the payload field holding the customer's name -
	// e.g. "name". The whole template is skipped (not an error) if this
	// field is absent from the merged payload when the transition fires -
	// customers.name is NOT NULL, so there is no meaningful customer
	// record to create without it; the same Rule-6-in-spirit reasoning
	// DeliveryTemplate.DestinationAddressField's own doc comment gives.
	NameField string
	// EmailField/PhoneField/AddressField name the payload fields holding
	// the corresponding customers column - all OPTIONAL, same "missing
	// just leaves that column NULL" contract DeliveryTemplate's own
	// optional fields have.
	EmailField   string
	PhoneField   string
	AddressField string
}

// InvoiceTemplate describes the draft invoice (with one line) a
// transition should create - the declarative shape a workflow definition
// carries; engine.go resolves it against one specific instance's payload
// at ExecuteTransition time, the same "resolved fresh per call, never
// baked in ahead of time" reasoning every other template in this file
// gives.
type InvoiceTemplate struct {
	// Description becomes the created line's description, with
	// "{{field}}" placeholders substituted from the transition's merged
	// instance payload - the same interpolateTemplate mechanism
	// JournalTemplate.Description already uses (e.g. "Sale of {{item}}").
	Description string
	// ProductField OPTIONALLY names the payload field holding the line's
	// product ID (a string UUID) - e.g. "product_id". Absent simply
	// leaves invoice_lines.product_id NULL, the same "missing optional
	// field means NULL column, not a skip" contract
	// DeliveryTemplate.OriginAddressField's own doc comment gives.
	ProductField string
	// QuantityField/UnitPriceField name the payload fields holding the
	// line's quantity and unit price - e.g. "quantity"/"unit_price".
	// BOTH must be present (and numeric) on a given transition call for
	// the whole template to apply; either absent skips it entirely (not
	// an error) - the same Rule-6-in-spirit reasoning
	// StockAdjustmentTemplate.ProductField's own doc comment gives: many
	// existing/legitimate callers of a transition that later gains an
	// InvoiceTemplate won't supply a price on every call, and this bridge
	// must never turn "the caller didn't send a price" into a broken
	// state transition.
	QuantityField  string
	UnitPriceField string
	// CustomerField OPTIONALLY names the payload field holding the
	// invoice's customer ID (a string UUID) - e.g. "customer_id". Absent
	// simply leaves invoices.customer_id NULL. Declared explicitly here
	// (unlike resolveJournalDescription's hardcoded customer_id
	// convention, engine.go) since a firm-defined workflow carrying an
	// InvoiceTemplate may use any field name for its own customer
	// reference, not just the one well-known convention stock_to_sale
	// happens to use.
	CustomerField string
}

// SalesOrderTemplate describes the sales order (with one line) a
// transition should create - the sixth bridge, same declarative shape
// InvoiceTemplate's own doc comment describes; engine.go resolves it
// against one specific instance's payload at ExecuteTransition time.
// stock_to_sale's record_sale carries this alongside its existing
// InvoiceTemplate - a sale now produces both a draft invoice AND a
// confirmed sales order in the same transaction, each independently
// resolved.
type SalesOrderTemplate struct {
	// Description becomes the created line's description, with
	// "{{field}}" placeholders substituted from the merged payload - same
	// interpolateTemplate mechanism InvoiceTemplate.Description uses.
	Description string
	// ProductField OPTIONALLY names the payload field holding the line's
	// product ID - same "missing leaves the column NULL" contract
	// InvoiceTemplate.ProductField's own doc comment gives.
	ProductField string
	// QuantityField/UnitPriceField name the payload fields holding the
	// line's quantity and unit price. BOTH must be present (and numeric)
	// for the whole template to apply - same Rule-6-in-spirit reasoning
	// InvoiceTemplate.QuantityField/UnitPriceField's own doc comment
	// gives: an existing record_sale caller with no price data must keep
	// working, with no sales order created.
	QuantityField  string
	UnitPriceField string
	// CustomerField OPTIONALLY names the payload field holding the
	// order's customer ID - same convention InvoiceTemplate.CustomerField
	// establishes.
	CustomerField string
	// ShippingAddressField OPTIONALLY names the payload field holding the
	// order's shipping address - absent simply leaves the column NULL,
	// the same "missing optional field means NULL column" contract every
	// other template's optional fields share.
	ShippingAddressField string
}

// PurchaseOrderTemplate describes the purchase order (with one line) a
// transition should create - the seventh bridge, SalesOrderTemplate's
// purchase-facing counterpart. purchase_order's own "send" transition
// carries this - see purchase_order.go's own doc comment for why "send"
// (not "receive") is the right transition for this bridge: sending is
// when the order itself becomes a real, structured commitment to a
// supplier, independent of the "receive" transition's own accounting
// bridge (which fires later, when goods actually arrive).
type PurchaseOrderTemplate struct {
	// Description becomes the created line's description, with
	// "{{field}}" placeholders substituted from the merged payload.
	Description string
	// ProductField OPTIONALLY names the payload field holding the line's
	// product ID.
	ProductField string
	// QuantityField/UnitPriceField name the payload fields holding the
	// line's quantity and unit price - BOTH must be present for the whole
	// template to apply, same Rule-6-in-spirit reasoning
	// SalesOrderTemplate's own doc comment gives.
	QuantityField  string
	UnitPriceField string
	// SupplierField OPTIONALLY names the payload field holding the
	// order's supplier ID.
	SupplierField string
}

// StockAdjustmentTemplate describes the stock_levels change a transition
// should apply - the declarative shape a workflow definition carries;
// engine.go resolves it against one specific instance's payload at
// ExecuteTransition time, the same "resolved fresh per call, never baked
// in ahead of time" reasoning JournalTemplate's own doc comment gives.
type StockAdjustmentTemplate struct {
	// ProductField names the field in the instance's payload that holds
	// the product's ID (a string UUID) - e.g. "product_id". If this field
	// is absent from the merged payload when a transition carrying this
	// template fires, the whole StockAdjustment is skipped for that call
	// (not an error - see engine.go's resolveStockAdjustment for why this
	// must stay non-breaking, the same Rule-6-in-spirit reasoning
	// resolveJournalLines already documents).
	ProductField string
	// QuantityField names the field in the instance's payload that holds
	// the quantity to adjust by (a positive number) - e.g. "quantity".
	// Direction (below) determines the sign actually applied.
	QuantityField string
	// Direction is "increase" or "decrease" - whether QuantityField's
	// value is added to or subtracted from stock_levels.quantity.
	Direction string
	// Reason is the stock_movements.reason this adjustment is recorded
	// under (e.g. "sale", "purchase") - see internal/inventory's own
	// stock_movements.reason doc comment (migrations/0011_inventory_core.up.sql)
	// for its open, app-validated vocabulary.
	Reason string
}

// JournalTemplate describes the journal entry a transition should post -
// the declarative shape a workflow definition carries; engine.go resolves
// it against one specific instance's payload at ExecuteTransition time,
// it is never evaluated ahead of time.
type JournalTemplate struct {
	// Description may include "{{field}}" placeholders, substituted from
	// the transition's merged instance payload (see engine.go's
	// interpolateTemplate) - e.g. "Sale of {{item}}".
	Description string
	Lines       []LineTemplate
}

// LineTemplate is one debit or credit leg of a JournalTemplate.
type LineTemplate struct {
	// AccountCode is resolved against the firm's own chart of accounts
	// (accounts.code, see internal/accounting) at posting time - not
	// validated when the workflow definition itself is defined, the same
	// deferred-validation reasoning FieldSpec.ReferenceDefinitionKey uses
	// for cross-referencing another workflow definition: a firm's chart of
	// accounts can change after the workflow was defined, so this is
	// re-resolved fresh on every transition, not baked in once.
	AccountCode string
	// Side is "debit" or "credit" (mirrors internal/accounting.Side).
	Side string
	// AmountField names the field in the instance's payload that holds
	// this line's amount. It MAY instead be two field names joined by
	// "*" (e.g. "quantity*unit_price") - the one payload-derived
	// computation this bridge supports natively, since "quantity times a
	// price field" is the realistic shape of a sale/purchase amount (see
	// stock_to_sale.go's own JournalTemplate for the concrete example) -
	// see engine.go's resolveAmountField for exactly how each form is
	// evaluated. If the named field(s) are absent from the instance's
	// payload, the whole JournalTemplate is skipped for that transition
	// (not an error - see resolveJournalLines's doc comment for why this
	// must stay non-breaking for a transition with no such field).
	AmountField string
}

// FieldType is a payload field's declared value type. Open Points item 35
// originally resolved this to just four scalar types
// (string/number/boolean/date); item 38 (the strain that resolution
// showed at "any sector" scale - docs/OPEN_POINTS.md) extends it with
// three more: FieldTypeEnum (a value constrained to a fixed set of
// strings), FieldTypeReference (a value that must be another workflow
// instance's ID, within the same firm), and FieldTypeArray (a JSON array
// of one of the four original scalar types - deliberately NOT of
// enum/reference/array itself, see Validate below and FieldSpec's
// ArrayItemType doc comment for why nesting is rejected rather than
// silently allowed through).
type FieldType string

const (
	FieldTypeString  FieldType = "string"
	FieldTypeNumber  FieldType = "number"
	FieldTypeBoolean FieldType = "boolean"
	FieldTypeDate    FieldType = "date"
	// FieldTypeEnum, FieldTypeReference, and FieldTypeArray are item 38's
	// additions - see FieldType's own doc comment above and FieldSpec's
	// Options/ReferenceDefinitionKey/ArrayItemType fields below for what
	// each one needs to be fully specified.
	FieldTypeEnum      FieldType = "enum"
	FieldTypeReference FieldType = "reference"
	FieldTypeArray     FieldType = "array"
	// FieldTypePerson (HR core batch) is FieldTypeReference's counterpart
	// for a non-workflow entity: a value that must be a real
	// internal/hr.Person's ID within the same firm - e.g.
	// TaskApprovalSpec's assignee_person_id. Unlike FieldTypeReference
	// (which resolves against another workflow_definitions/workflow_instances
	// pair), this resolves against the `people` table directly - see
	// engine.go's checkPersonField. Needs no extra FieldSpec fields the
	// way Options/ReferenceDefinitionKey/ArrayItemType do, since there's
	// only one `people` table to reference, not a choice of target.
	FieldTypePerson FieldType = "person"
	// FieldTypeProduct and FieldTypeSupplier (Inventory management batch)
	// are FieldTypePerson's counterparts for the two new cross-module
	// entities this batch introduces: a value that must be a real
	// internal/inventory.Product's or Supplier's ID within the same firm -
	// e.g. stock_to_sale's product_id, purchase_order's supplier_id. Same
	// reasoning as FieldTypePerson: resolves against one specific table
	// directly (products or suppliers), not a choice of target the way
	// FieldTypeReference has - see engine.go's checkProductField/
	// checkSupplierField.
	FieldTypeProduct  FieldType = "product"
	FieldTypeSupplier FieldType = "supplier"
	// FieldTypeDelivery and FieldTypeCustomer (Logistics/CRM batch) are
	// FieldTypePerson's counterparts for this batch's two new cross-module
	// entities: a value that must be a real internal/logistics.Delivery's
	// or internal/crm.Customer's ID within the same firm - e.g.
	// stock_to_sale's customer_id. Same reasoning as FieldTypePerson/
	// FieldTypeProduct: resolves against one specific table directly, not
	// a choice of target - see engine.go's checkDeliveryField/
	// checkCustomerField.
	FieldTypeDelivery FieldType = "delivery"
	FieldTypeCustomer FieldType = "customer"
	// FieldTypeProject (Multi-location CRM/project management batch) is
	// FieldTypePerson's counterpart for this batch's new cross-module
	// entity: a value that must be a real internal/project.Project's ID
	// within the same firm - e.g. TaskApprovalSpec's optional project_id.
	// Same reasoning as FieldTypePerson/FieldTypeCustomer: resolves
	// against one specific table (projects) directly, not a choice of
	// target - see validation.go's own projectValidator.
	FieldTypeProject FieldType = "project"
	// FieldTypeAsset (Asset management/maintenance/facility operations
	// batch) is FieldTypePerson's counterpart for this batch's new
	// cross-module entity: a value that must be a real
	// internal/asset.Asset's ID within the same firm - e.g. a repair
	// workflow instance referencing which asset needs repair, a delivery
	// referencing which vehicle is used. Same reasoning as
	// FieldTypePerson/FieldTypeProject: resolves against one specific
	// table (assets) directly, not a choice of target - see
	// validation.go's own assetValidator.
	FieldTypeAsset FieldType = "asset"
	// FieldTypeContract (Contracts management/document workflows/legal
	// foundation batch) is FieldTypePerson's counterpart for this batch's
	// new cross-module entity: a value that must be a real
	// internal/contracts.Contract's ID within the same firm - e.g. a
	// sales order workflow instance referencing its governing contract,
	// a procurement order referencing a supplier contract. Same reasoning
	// as FieldTypePerson/FieldTypeAsset: resolves against one specific
	// table (contract_registry) directly, not a choice of target - see
	// validation.go's own contractValidator.
	FieldTypeContract FieldType = "contract"
)

// FieldSpec is one declared field in a workflow definition's instance
// payload schema (Open Points item 35, extended by item 38): a name, a
// declared FieldType, and whether CreateInstance must see it present.
// This is deliberately a flat Go struct slice, not a JSON-Schema-shaped or
// DSL-based structure - item 35's question 2 resolved in favor of
// "defined the same way the rest of a workflow definition is defined
// today", i.e. as another field on DefinitionSpec, going through the
// exact same DefineWorkflow/DefineWorkflowForFirm path as States/
// Transitions already do, not a separate mechanism or a wizard-only
// concept (item 12's wizard question tree remains a separate, still-
// undesigned thing).
type FieldSpec struct {
	Name     string
	Type     FieldType
	Required bool

	// Options is only meaningful when Type == FieldTypeEnum: the fixed set
	// of string values CreateInstance's payload validation accepts for
	// this field (checkFieldType in engine.go). Validate rejects an enum
	// field with zero options, a duplicate option, or an empty-string
	// option - and rejects Options being set on any non-enum field, so a
	// spec can't carry a stray, silently-ignored value.
	Options []string

	// ReferenceDefinitionKey is only meaningful when Type ==
	// FieldTypeReference: the workflow_definitions.key (e.g.
	// "stock_to_sale") this field's value must resolve to an instance of,
	// within the same firm - see engine.go's checkReferenceField. Validate
	// only confirms this is non-empty for a reference field (and empty for
	// every other field type); it CANNOT confirm the key actually names a
	// real definition for the firm, because Validate is a pure, DB-free
	// function with no firm context (see Validate's own doc comment on
	// this exact point) - that existence check is done by
	// DefineWorkflowTx instead, which does have a transaction and firmID
	// in scope (engine.go).
	ReferenceDefinitionKey string

	// ArrayItemType is only meaningful when Type == FieldTypeArray: which
	// of the four scalar FieldTypes (string/number/boolean/date) each
	// array element must satisfy. Deliberately restricted to those four -
	// Validate rejects FieldTypeEnum, FieldTypeReference, or
	// FieldTypeArray itself as an ArrayItemType (no nested arrays, no
	// arrays of references/enums), matching this batch's explicit scope
	// boundary. Stored as a plain string (not FieldType) purely as this
	// struct's wire-adjacent shape - see engine.go's payloadFieldRow,
	// which stores it the same way.
	ArrayItemType string
}

// DefinitionSpec is a full workflow definition: its states, the
// transitions between them, and the permission required to create a new
// instance of it (instance creation has no from-state, so it isn't a
// transition - see internal/workflow's package docs).
//
// Fields is OPTIONAL and additive: a DefinitionSpec with a nil or empty
// Fields (every DefinitionSpec that existed before Open Points item 35,
// including StockToSaleSpec/CustomerPipelineSpec today) keeps behaving
// exactly as it always has - CreateInstance's payload stays a completely
// freeform map[string]any, no field is required, no type is checked. Only
// a definition that deliberately sets Fields opts into CreateInstance
// validating new instances' payloads against it (see engine.go's
// CreateInstance and validatePayload) - existing instances are never
// re-validated against a schema added after they were created (item 35's
// question 4): CurrentState/ListInstances/every other read path in this
// package never consults Fields at all, only CreateInstance does, and
// only at the moment a new instance is created.
type DefinitionSpec struct {
	Key              string
	Name             string
	CreatePermission PermissionSpec
	States           []StateSpec
	Transitions      []TransitionSpec
	Fields           []FieldSpec
}

// Validate checks the spec is structurally sound before anything is
// written to the database: exactly one initial state, unique state keys,
// every transition referencing a state that actually exists, and no two
// transitions sharing a (from-state, action) pair - the same constraint
// migrations/0003_workflow_engine.up.sql enforces at the database level.
func (d DefinitionSpec) Validate() error {
	if d.Key == "" {
		return fmt.Errorf("workflow definition key must not be empty")
	}
	if d.Name == "" {
		return fmt.Errorf("workflow definition name must not be empty")
	}
	if d.CreatePermission.Key == "" {
		return fmt.Errorf("workflow definition %q: create permission key must not be empty", d.Key)
	}
	if len(d.States) == 0 {
		return fmt.Errorf("workflow definition %q: must define at least one state", d.Key)
	}

	stateKeys := make(map[string]struct{}, len(d.States))
	initialCount := 0
	for _, s := range d.States {
		if s.Key == "" {
			return fmt.Errorf("workflow definition %q: state key must not be empty", d.Key)
		}
		if _, exists := stateKeys[s.Key]; exists {
			return fmt.Errorf("workflow definition %q: duplicate state key %q", d.Key, s.Key)
		}
		stateKeys[s.Key] = struct{}{}
		if s.IsInitial {
			initialCount++
		}
	}
	if initialCount != 1 {
		return fmt.Errorf("workflow definition %q: must have exactly one initial state, found %d", d.Key, initialCount)
	}

	transitionKeys := make(map[string]struct{}, len(d.Transitions))
	for _, t := range d.Transitions {
		if t.ActionKey == "" {
			return fmt.Errorf("workflow definition %q: transition action key must not be empty", d.Key)
		}
		if _, ok := stateKeys[t.FromStateKey]; !ok {
			return fmt.Errorf("workflow definition %q: transition %q references unknown from-state %q", d.Key, t.ActionKey, t.FromStateKey)
		}
		if _, ok := stateKeys[t.ToStateKey]; !ok {
			return fmt.Errorf("workflow definition %q: transition %q references unknown to-state %q", d.Key, t.ActionKey, t.ToStateKey)
		}
		if t.Permission.Key == "" {
			return fmt.Errorf("workflow definition %q: transition %q must have a permission key", d.Key, t.ActionKey)
		}
		for _, e := range t.Effects {
			if err := validateEffect(d.Key, t.ActionKey, e); err != nil {
				return err
			}
		}
		dedupeKey := t.FromStateKey + "\x00" + t.ActionKey
		if _, exists := transitionKeys[dedupeKey]; exists {
			return fmt.Errorf("workflow definition %q: duplicate transition action %q from state %q", d.Key, t.ActionKey, t.FromStateKey)
		}
		transitionKeys[dedupeKey] = struct{}{}
	}

	if len(d.Fields) > 0 {
		fieldNames := make(map[string]struct{}, len(d.Fields))
		for _, f := range d.Fields {
			if f.Name == "" {
				return fmt.Errorf("workflow definition %q: field name must not be empty", d.Key)
			}
			if _, exists := fieldNames[f.Name]; exists {
				return fmt.Errorf("workflow definition %q: duplicate field name %q", d.Key, f.Name)
			}
			fieldNames[f.Name] = struct{}{}
			if err := validateFieldShape(d.Key, f); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateJournalTemplate checks one TransitionSpec's optional
// JournalTemplate is structurally sound: a non-empty description, at
// least two lines (an entry can never balance with fewer), and every
// line naming an account code, a debit/credit side, and an amount field.
// It deliberately cannot check that AccountCode resolves to a real
// account, or that the amounts it would compute actually balance -
// both require a firm's live chart of accounts and a real instance
// payload, neither of which exists at spec-definition time (the same
// "pure, DB-free function" limitation FieldSpec.ReferenceDefinitionKey's
// own doc comment describes) - see engine.go's resolveJournalLines/
// internal/accounting.PostJournalEntryTx for where those checks actually
// run, at posting time.
func validateJournalTemplate(defKey, actionKey string, jt JournalTemplate) error {
	if jt.Description == "" {
		return fmt.Errorf("workflow definition %q: transition %q: journal template description must not be empty", defKey, actionKey)
	}
	if len(jt.Lines) < 2 {
		return fmt.Errorf("workflow definition %q: transition %q: journal template must have at least two lines", defKey, actionKey)
	}
	for i, l := range jt.Lines {
		if l.AccountCode == "" {
			return fmt.Errorf("workflow definition %q: transition %q: journal line %d: account code must not be empty", defKey, actionKey, i)
		}
		if l.Side != "debit" && l.Side != "credit" {
			return fmt.Errorf("workflow definition %q: transition %q: journal line %d: side must be \"debit\" or \"credit\", got %q", defKey, actionKey, i, l.Side)
		}
		if l.AmountField == "" {
			return fmt.Errorf("workflow definition %q: transition %q: journal line %d: amount field must not be empty", defKey, actionKey, i)
		}
	}
	return nil
}

// validateFieldShape checks one FieldSpec's structural rules - split out
// of Validate itself (rather than inlined in its loop) once item 38 added
// three more type-specific shapes to check, each with its own
// Options/ReferenceDefinitionKey/ArrayItemType side-constraints. defKey is
// only used to prefix error messages the same way every other error in
// Validate is prefixed.
//
// What this function deliberately CANNOT check: whether a
// FieldTypeReference field's ReferenceDefinitionKey actually names a real
// workflow_definitions row for the firm - Validate (and this helper) are
// pure, DB-free functions with no firm/DB context, called before any
// transaction exists in some callers (e.g. a bare `spec.Validate()` a test
// or a future caller might run standalone). That existence check happens
// in DefineWorkflowTx instead (engine.go), which does have a live
// transaction and firmID - see FieldSpec.ReferenceDefinitionKey's own doc
// comment for the full reasoning.
func validateFieldShape(defKey string, f FieldSpec) error {
	switch f.Type {
	case FieldTypeString, FieldTypeNumber, FieldTypeBoolean, FieldTypeDate:
		if len(f.Options) > 0 {
			return fmt.Errorf("workflow definition %q: field %q: options is only meaningful for an enum field", defKey, f.Name)
		}
		if f.ReferenceDefinitionKey != "" {
			return fmt.Errorf("workflow definition %q: field %q: referenceDefinitionKey is only meaningful for a reference field", defKey, f.Name)
		}
		if f.ArrayItemType != "" {
			return fmt.Errorf("workflow definition %q: field %q: arrayItemType is only meaningful for an array field", defKey, f.Name)
		}
		return nil

	case FieldTypeEnum:
		if len(f.Options) == 0 {
			return fmt.Errorf("workflow definition %q: field %q: an enum field must declare at least one option", defKey, f.Name)
		}
		seen := make(map[string]struct{}, len(f.Options))
		for _, opt := range f.Options {
			if opt == "" {
				return fmt.Errorf("workflow definition %q: field %q: enum options must not be empty strings", defKey, f.Name)
			}
			if _, dup := seen[opt]; dup {
				return fmt.Errorf("workflow definition %q: field %q: duplicate enum option %q", defKey, f.Name, opt)
			}
			seen[opt] = struct{}{}
		}
		if f.ReferenceDefinitionKey != "" {
			return fmt.Errorf("workflow definition %q: field %q: referenceDefinitionKey is only meaningful for a reference field", defKey, f.Name)
		}
		if f.ArrayItemType != "" {
			return fmt.Errorf("workflow definition %q: field %q: arrayItemType is only meaningful for an array field", defKey, f.Name)
		}
		return nil

	case FieldTypeReference:
		if f.ReferenceDefinitionKey == "" {
			return fmt.Errorf("workflow definition %q: field %q: a reference field must declare referenceDefinitionKey", defKey, f.Name)
		}
		if len(f.Options) > 0 {
			return fmt.Errorf("workflow definition %q: field %q: options is only meaningful for an enum field", defKey, f.Name)
		}
		if f.ArrayItemType != "" {
			return fmt.Errorf("workflow definition %q: field %q: arrayItemType is only meaningful for an array field", defKey, f.Name)
		}
		return nil

	case FieldTypePerson, FieldTypeProduct, FieldTypeSupplier, FieldTypeDelivery, FieldTypeCustomer, FieldTypeProject, FieldTypeAsset, FieldTypeContract:
		// No extra fields needed - same reasoning FieldTypePerson's own doc
		// comment gives (only one target table to reference per type,
		// unlike FieldTypeReference's choice of target workflow definition).
		if len(f.Options) > 0 {
			return fmt.Errorf("workflow definition %q: field %q: options is only meaningful for an enum field", defKey, f.Name)
		}
		if f.ReferenceDefinitionKey != "" {
			return fmt.Errorf("workflow definition %q: field %q: referenceDefinitionKey is only meaningful for a reference field", defKey, f.Name)
		}
		if f.ArrayItemType != "" {
			return fmt.Errorf("workflow definition %q: field %q: arrayItemType is only meaningful for an array field", defKey, f.Name)
		}
		return nil

	case FieldTypeArray:
		// Deliberately restricted to the four original scalar types - no
		// nested arrays, no arrays of enums, no arrays of references. This
		// is the batch's explicit scope boundary, enforced here at
		// spec-definition time rather than left to be silently mishandled
		// (or discovered) at CreateInstance/runtime time.
		switch FieldType(f.ArrayItemType) {
		case FieldTypeString, FieldTypeNumber, FieldTypeBoolean, FieldTypeDate:
			// valid
		case FieldTypeArray:
			return fmt.Errorf("workflow definition %q: field %q: nested arrays are not supported - arrayItemType must not itself be \"array\"", defKey, f.Name)
		case FieldTypeReference:
			return fmt.Errorf("workflow definition %q: field %q: arrays of references are not supported - arrayItemType must not be \"reference\"", defKey, f.Name)
		case FieldTypeEnum:
			return fmt.Errorf("workflow definition %q: field %q: arrays of enums are not supported - arrayItemType must not be \"enum\"", defKey, f.Name)
		default:
			return fmt.Errorf("workflow definition %q: field %q: arrayItemType %q must be one of string/number/boolean/date", defKey, f.Name, f.ArrayItemType)
		}
		if len(f.Options) > 0 {
			return fmt.Errorf("workflow definition %q: field %q: options is only meaningful for an enum field", defKey, f.Name)
		}
		if f.ReferenceDefinitionKey != "" {
			return fmt.Errorf("workflow definition %q: field %q: referenceDefinitionKey is only meaningful for a reference field", defKey, f.Name)
		}
		return nil

	default:
		return fmt.Errorf("workflow definition %q: field %q has unknown type %q (must be one of string/number/boolean/date/enum/reference/array/person/product/supplier/delivery/customer)", defKey, f.Name, f.Type)
	}
}

// validateStockAdjustmentTemplate checks one TransitionSpec's optional
// StockAdjustmentTemplate is structurally sound: a product field name, a
// quantity field name, a valid direction, and a non-empty reason. Same
// "cannot check the referenced product actually exists here" limitation
// validateJournalTemplate's own doc comment gives for account codes - that
// happens at ExecuteTransition time instead (engine.go's
// resolveStockAdjustment / internal/inventory.AdjustStockTx), against a
// real instance payload and a real firm's product catalog, neither of
// which exists at spec-definition time.
func validateStockAdjustmentTemplate(defKey, actionKey string, sat StockAdjustmentTemplate) error {
	if sat.ProductField == "" {
		return fmt.Errorf("workflow definition %q: transition %q: stock adjustment must name a product field", defKey, actionKey)
	}
	if sat.QuantityField == "" {
		return fmt.Errorf("workflow definition %q: transition %q: stock adjustment must name a quantity field", defKey, actionKey)
	}
	if sat.Direction != "increase" && sat.Direction != "decrease" {
		return fmt.Errorf("workflow definition %q: transition %q: stock adjustment direction must be \"increase\" or \"decrease\", got %q", defKey, actionKey, sat.Direction)
	}
	if sat.Reason == "" {
		return fmt.Errorf("workflow definition %q: transition %q: stock adjustment must have a reason", defKey, actionKey)
	}
	return nil
}

// validateDeliveryTemplate checks one TransitionSpec's optional
// DeliveryTemplate is structurally sound: a destination-address field
// name. Same "cannot check the referenced field's value at spec-
// definition time" limitation every other template validator in this file
// documents - that happens at ExecuteTransition time instead (engine.go's
// resolveDelivery), against a real instance payload.
func validateDeliveryTemplate(defKey, actionKey string, dt DeliveryTemplate) error {
	if dt.DestinationAddressField == "" {
		return fmt.Errorf("workflow definition %q: transition %q: delivery template must name a destination address field", defKey, actionKey)
	}
	return nil
}

// validateCustomerTemplate checks one TransitionSpec's optional
// CustomerTemplate is structurally sound: a name field name. Same
// "cannot check the referenced field's value at spec-definition time"
// limitation every other template validator in this file documents.
func validateCustomerTemplate(defKey, actionKey string, ct CustomerTemplate) error {
	if ct.NameField == "" {
		return fmt.Errorf("workflow definition %q: transition %q: customer template must name a name field", defKey, actionKey)
	}
	return nil
}

// validateInvoiceTemplate checks one TransitionSpec's optional
// InvoiceTemplate is structurally sound: a non-empty description, a
// quantity field name, and a unit price field name. Same "cannot check
// the referenced field's value at spec-definition time" limitation every
// other template validator in this file documents - that happens at
// ExecuteTransition time instead (engine.go's resolveInvoice), against a
// real instance payload.
func validateInvoiceTemplate(defKey, actionKey string, it InvoiceTemplate) error {
	if it.Description == "" {
		return fmt.Errorf("workflow definition %q: transition %q: invoice template description must not be empty", defKey, actionKey)
	}
	if it.QuantityField == "" {
		return fmt.Errorf("workflow definition %q: transition %q: invoice template must name a quantity field", defKey, actionKey)
	}
	if it.UnitPriceField == "" {
		return fmt.Errorf("workflow definition %q: transition %q: invoice template must name a unit price field", defKey, actionKey)
	}
	return nil
}

// validateSalesOrderTemplate checks one TransitionSpec's optional
// SalesOrderTemplate is structurally sound: a non-empty description, a
// quantity field name, and a unit price field name - same shape
// validateInvoiceTemplate checks for InvoiceTemplate.
func validateSalesOrderTemplate(defKey, actionKey string, sot SalesOrderTemplate) error {
	if sot.Description == "" {
		return fmt.Errorf("workflow definition %q: transition %q: sales order template description must not be empty", defKey, actionKey)
	}
	if sot.QuantityField == "" {
		return fmt.Errorf("workflow definition %q: transition %q: sales order template must name a quantity field", defKey, actionKey)
	}
	if sot.UnitPriceField == "" {
		return fmt.Errorf("workflow definition %q: transition %q: sales order template must name a unit price field", defKey, actionKey)
	}
	return nil
}

// validatePurchaseOrderTemplate checks one TransitionSpec's optional
// PurchaseOrderTemplate is structurally sound - same shape
// validateSalesOrderTemplate checks for SalesOrderTemplate.
func validatePurchaseOrderTemplate(defKey, actionKey string, pot PurchaseOrderTemplate) error {
	if pot.Description == "" {
		return fmt.Errorf("workflow definition %q: transition %q: purchase order template description must not be empty", defKey, actionKey)
	}
	if pot.QuantityField == "" {
		return fmt.Errorf("workflow definition %q: transition %q: purchase order template must name a quantity field", defKey, actionKey)
	}
	if pot.UnitPriceField == "" {
		return fmt.Errorf("workflow definition %q: transition %q: purchase order template must name a unit price field", defKey, actionKey)
	}
	return nil
}

// validateEffect dispatches one TransitionEffect to the validator for its
// own Kind - the Effects-refactor's counterpart to what used to be five
// separate `if t.Journal != nil { validateJournalTemplate(...) }`-shaped
// blocks in Validate's own transition loop. An unknown Kind (e.g. a typo
// in a hand-authored DefinitionSpec, or a malicious/malformed value in a
// firm-supplied `POST .../workflow-definitions` request body) is
// rejected outright - Effects is a closed vocabulary (EffectKind's own
// doc comment), not something a firm extends. A Payload that fails to
// unmarshal into its Kind's own template struct is reported the same way
// a malformed request body anywhere else in this codebase is.
func validateEffect(defKey, actionKey string, e TransitionEffect) error {
	switch e.Kind {
	case EffectKindJournal:
		var jt JournalTemplate
		if err := json.Unmarshal(e.Payload, &jt); err != nil {
			return fmt.Errorf("workflow definition %q: transition %q: invalid journal effect payload: %w", defKey, actionKey, err)
		}
		return validateJournalTemplate(defKey, actionKey, jt)
	case EffectKindStock:
		var sat StockAdjustmentTemplate
		if err := json.Unmarshal(e.Payload, &sat); err != nil {
			return fmt.Errorf("workflow definition %q: transition %q: invalid stock effect payload: %w", defKey, actionKey, err)
		}
		return validateStockAdjustmentTemplate(defKey, actionKey, sat)
	case EffectKindDelivery:
		var dt DeliveryTemplate
		if err := json.Unmarshal(e.Payload, &dt); err != nil {
			return fmt.Errorf("workflow definition %q: transition %q: invalid delivery effect payload: %w", defKey, actionKey, err)
		}
		return validateDeliveryTemplate(defKey, actionKey, dt)
	case EffectKindCustomer:
		var ct CustomerTemplate
		if err := json.Unmarshal(e.Payload, &ct); err != nil {
			return fmt.Errorf("workflow definition %q: transition %q: invalid customer effect payload: %w", defKey, actionKey, err)
		}
		return validateCustomerTemplate(defKey, actionKey, ct)
	case EffectKindInvoice:
		var it InvoiceTemplate
		if err := json.Unmarshal(e.Payload, &it); err != nil {
			return fmt.Errorf("workflow definition %q: transition %q: invalid invoice effect payload: %w", defKey, actionKey, err)
		}
		return validateInvoiceTemplate(defKey, actionKey, it)
	case EffectKindSalesOrder:
		var sot SalesOrderTemplate
		if err := json.Unmarshal(e.Payload, &sot); err != nil {
			return fmt.Errorf("workflow definition %q: transition %q: invalid sales order effect payload: %w", defKey, actionKey, err)
		}
		return validateSalesOrderTemplate(defKey, actionKey, sot)
	case EffectKindPurchaseOrder:
		var pot PurchaseOrderTemplate
		if err := json.Unmarshal(e.Payload, &pot); err != nil {
			return fmt.Errorf("workflow definition %q: transition %q: invalid purchase order effect payload: %w", defKey, actionKey, err)
		}
		return validatePurchaseOrderTemplate(defKey, actionKey, pot)
	default:
		return fmt.Errorf("workflow definition %q: transition %q: unknown effect kind %q (must be one of journal/stock/delivery/customer/invoice/salesOrder/purchaseOrder)", defKey, actionKey, e.Kind)
	}
}

// PermissionKeys lists every permission key this spec references (its
// create permission plus every transition's permission), in declaration
// order. Used by callers that need to grant a role every capability a
// given workflow definition introduces - e.g. the firm-creation wizard
// granting its default role exactly what SeedStockToSaleWorkflow just
// provisioned, without hardcoding those keys a second time.
func (d DefinitionSpec) PermissionKeys() []string {
	keys := make([]string, 0, 1+len(d.Transitions))
	keys = append(keys, d.CreatePermission.Key)
	for _, t := range d.Transitions {
		keys = append(keys, t.Permission.Key)
	}
	return keys
}
