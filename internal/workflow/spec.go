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

import "fmt"

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
type TransitionSpec struct {
	FromStateKey string
	ToStateKey   string
	ActionKey    string
	Name         string
	Permission   PermissionSpec
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
		return fmt.Errorf("workflow definition %q: field %q has unknown type %q (must be one of string/number/boolean/date/enum/reference/array)", defKey, f.Name, f.Type)
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
