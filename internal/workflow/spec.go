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

// FieldType is a payload field's declared value type - deliberately just
// these four (Open Points item 35's resolution): no nested objects, no
// arrays, no enum/pattern constraints. A workflow whose payload needs
// something richer than "a string/number/boolean/date, required or not"
// is out of this mechanism's scope for now - see docs/OPEN_POINTS.md item
// 35's remaining note on a richer type system.
type FieldType string

const (
	FieldTypeString  FieldType = "string"
	FieldTypeNumber  FieldType = "number"
	FieldTypeBoolean FieldType = "boolean"
	FieldTypeDate    FieldType = "date"
)

// FieldSpec is one declared field in a workflow definition's instance
// payload schema (Open Points item 35): a name, a declared FieldType, and
// whether CreateInstance must see it present. This is deliberately a flat
// Go struct slice, not a JSON-Schema-shaped or DSL-based structure -
// item 35's question 2 resolved in favor of "defined the same way the
// rest of a workflow definition is defined today", i.e. as another field
// on DefinitionSpec, going through the exact same DefineWorkflow/
// DefineWorkflowForFirm path as States/Transitions already do, not a
// separate mechanism or a wizard-only concept (item 12's wizard question
// tree remains a separate, still-undesigned thing).
type FieldSpec struct {
	Name     string
	Type     FieldType
	Required bool
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
			switch f.Type {
			case FieldTypeString, FieldTypeNumber, FieldTypeBoolean, FieldTypeDate:
				// valid
			default:
				return fmt.Errorf("workflow definition %q: field %q has unknown type %q (must be one of string/number/boolean/date)", d.Key, f.Name, f.Type)
			}
		}
	}

	return nil
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
