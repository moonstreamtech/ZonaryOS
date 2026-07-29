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

// DefinitionSpec is a full workflow definition: its states, the
// transitions between them, and the permission required to create a new
// instance of it (instance creation has no from-state, so it isn't a
// transition - see internal/workflow's package docs).
type DefinitionSpec struct {
	Key              string
	Name             string
	CreatePermission PermissionSpec
	States           []StateSpec
	Transitions      []TransitionSpec
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
