// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Rule engine data model (Open Points item 7): Vision §3's "Autonomous
// Decision Layer" - unlimited-depth parametric "if this condition occurs ->
// automatically do this action" rules attached to a workflow definition.
// Conditions are stored as an expression tree (ExpressionNode), not a flat
// list, because they must compose arbitrarily - including a cross-workflow
// join ("this instance's payload AND that other-definition instance's
// current state"), the concrete case Vision §3 names. Evaluate is a pure
// function over that tree; EvaluateRules is the impure driver that loads a
// definition's enabled rules and runs their actions.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// Trigger identifies when a rule is evaluated. TriggerSchedule (this
// batch, Open Points item 7's remaining "(2) Schedule-triggered rules"
// gap) is a deliberately PARTIAL implementation of Vision §3's "if sensor
// temperature exceeds Y" style timer-driven rule, not the full thing -
// see scheduler.go's own doc comment for the exact scope boundary
// (no condition tree, notify-only actions). TriggerOnEdgeEvent (NATS
// JetStream/Edge Agent completion batch) is the same kind of partial,
// notify-only implementation, for the same reason - see this file's own
// EvaluateEdgeEventRules doc comment.
type Trigger string

const (
	TriggerOnCreate     Trigger = "on_create"
	TriggerOnTransition Trigger = "on_transition"
	// TriggerSchedule fires on a timer (Rule.ScheduleInterval), driven by
	// scheduler.go's background goroutine rather than CreateInstance/
	// ExecuteTransition - see that file's own doc comment.
	TriggerSchedule Trigger = "schedule"
	// TriggerOnEdgeEvent fires when internal/edgeagent.RecordEvent
	// records a new edge_events row - see EvaluateEdgeEventRules' own
	// doc comment for the wiring point and why it's a separate driver
	// from EvaluateRules.
	TriggerOnEdgeEvent Trigger = "on_edge_event"
)

// LogicOp is a logic node's boolean operator.
type LogicOp string

const (
	LogicAnd LogicOp = "and"
	LogicOr  LogicOp = "or"
	LogicNot LogicOp = "not"
)

// ConditionType discriminates a condition leaf's kind - see ExpressionNode.
type ConditionType string

const (
	// ConditionField compares a field from this instance's own payload
	// against a literal value.
	ConditionField ConditionType = "field"
	// ConditionState is the cross-workflow condition: resolves a reference
	// field on this instance's payload to another instance (of
	// DefinitionKey) and checks that instance's current state.
	ConditionState ConditionType = "state"
	// ConditionFieldCompare compares two fields from this instance's own
	// payload against each other.
	ConditionFieldCompare ConditionType = "field_compare"
	// ConditionEdgeEvent (NATS JetStream/Edge Agent completion batch) is
	// true when an edge_events row exists for the given agent+event type
	// within the last EdgeEventWithinMinutes minutes - usable by ANY
	// rule's condition tree (on_create/on_transition rules can gate a
	// transition on "did a matching edge event show up recently", not
	// just on_edge_event-triggered rules themselves), the same "condition
	// leaf reads via ec.Pool, independent of which trigger fired
	// evaluation" shape ConditionState already establishes.
	ConditionEdgeEvent ConditionType = "edge_event"
)

// CompareOp is the comparison operator a "field" or "field_compare" leaf
// uses. "contains" is only meaningful for "field" (a substring check
// against a literal), not "field_compare".
type CompareOp string

const (
	OpEq       CompareOp = "eq"
	OpNeq      CompareOp = "neq"
	OpLt       CompareOp = "lt"
	OpGt       CompareOp = "gt"
	OpLte      CompareOp = "lte"
	OpGte      CompareOp = "gte"
	OpContains CompareOp = "contains"
)

// ExpressionNode is one node in a rule's condition tree - either a logic
// node (Type is empty, Op/Children are set) or a condition leaf (Type is
// set, identifying which of the leaf-specific fields below apply). This is
// deliberately one flat struct, not a Go interface with per-kind
// implementations: it round-trips through the workflow_rules.condition_tree
// jsonb column with encoding/json's ordinary struct (de)serialization, no
// custom MarshalJSON/UnmarshalJSON needed, the same "flat struct, all
// fields optional, omitempty" shape FieldSpec (spec.go) already uses for
// item 38's type-specific extras.
type ExpressionNode struct {
	// Op is a logic node's operator ("and"/"or"/"not") when Type is empty,
	// or a leaf's comparison operator ("eq"/"neq"/...) when Type is
	// "field" or "field_compare". The same wire key ("op") serves both
	// roles - which one applies is unambiguous from Type/Children, exactly
	// the shape the design brief specifies for each node kind.
	Op       string           `json:"op,omitempty"`
	Children []ExpressionNode `json:"children,omitempty"`

	// Type, when non-empty, marks this node as a condition leaf and
	// selects which of the fields below are meaningful.
	Type ConditionType `json:"type,omitempty"`

	// "field" leaf: Field/Op/Value.
	Field string `json:"field,omitempty"`
	Value any    `json:"value,omitempty"`

	// "state" leaf: DefinitionKey/InstanceIDField/State.
	DefinitionKey   string `json:"definitionKey,omitempty"`
	InstanceIDField string `json:"instanceIdField,omitempty"`
	State           string `json:"state,omitempty"`

	// "field_compare" leaf: FieldA/Op/FieldB.
	FieldA string `json:"fieldA,omitempty"`
	FieldB string `json:"fieldB,omitempty"`

	// "edge_event" leaf: AgentID (an edge_agents.id, as a string - not
	// resolved from the payload the way "state"'s InstanceIDField is,
	// since an edge event's own triggering agent is already known and
	// distinct from whatever instance/event payload happens to be in
	// scope), EventType, and EdgeEventWithinMinutes (the lookback
	// window, required positive).
	AgentID                string `json:"agentId,omitempty"`
	EventType              string `json:"eventType,omitempty"`
	EdgeEventWithinMinutes int    `json:"withinMinutes,omitempty"`
}

// isLogicNode reports whether n is a logic node (Type unset) as opposed to
// a condition leaf (Type set to one of ConditionField/ConditionState/
// ConditionFieldCompare).
func (n ExpressionNode) isLogicNode() bool {
	return n.Type == ""
}

// ActionType discriminates one entry in a Rule's Actions list.
type ActionType string

const (
	// ActionTransition autonomously executes a workflow transition -
	// deterministic, no-ML, per Vision §3.
	ActionTransition ActionType = "transition"
	// ActionNotify writes a structured entry to the audit log - the one
	// notification channel available this batch (email/push are Open
	// Points item 17, still deferred).
	ActionNotify ActionType = "notify"
	// ActionSetField updates a field in this instance's payload.
	ActionSetField ActionType = "set_field"
)

// NotifyChannel is an ActionNotify action's delivery channel. audit_log is
// the only channel implemented this batch - see ActionNotify's doc comment.
type NotifyChannel string

const NotifyChannelAuditLog NotifyChannel = "audit_log"

// Action is one entry in a Rule's Actions list, executed in order once its
// rule's ConditionTree evaluates true. Like ExpressionNode, a flat struct
// with type-specific optional fields rather than a Go interface, for the
// same plain-jsonb-round-trip reason.
type Action struct {
	Type ActionType `json:"type"`

	// ActionTransition: the workflow_transitions.action_key to execute on
	// the triggering instance.
	ActionKey string `json:"actionKey,omitempty"`

	// ActionNotify: Channel (only NotifyChannelAuditLog today) and a
	// MessageTemplate - "{{fieldName}}" placeholders are substituted with
	// the triggering instance's payload values (see renderMessageTemplate).
	Channel         NotifyChannel `json:"channel,omitempty"`
	MessageTemplate string        `json:"messageTemplate,omitempty"`

	// ActionSetField: the payload field to set and the literal value to
	// set it to.
	Field string `json:"field,omitempty"`
	Value any    `json:"value,omitempty"`
}

// Rule is one stored workflow_rules row - Vision §3's "if this condition
// occurs -> automatically do this action", scoped to one workflow
// definition within one firm.
type Rule struct {
	ID            uuid.UUID
	FirmID        uuid.UUID
	DefinitionKey string
	Name          string
	Trigger       Trigger
	ConditionTree ExpressionNode
	Actions       []Action
	// Autonomous implements Never-Violate Rule 8's per-rule autonomous/
	// human-approval toggle: true runs Actions immediately when
	// ConditionTree matches; false only records a "pending approval"
	// audit_log entry (see EvaluateRules) - there is no approval-queue UI
	// to actually run a pending rule yet, flagged in docs/OPEN_POINTS.md.
	Autonomous bool
	Enabled    bool
	// ScheduleInterval is a Go time.Duration string (e.g. "1h", "24h") -
	// required, and required to parse as a positive duration, when
	// Trigger is TriggerSchedule; ignored (may be nil) for every other
	// trigger. See scheduler.go for how this drives re-scheduling after
	// each run.
	ScheduleInterval *string
	CreatedAt        time.Time
}

// ErrInvalidRule means a Rule's ConditionTree or Actions is structurally
// malformed (e.g. an "and" node with no children, an action with an
// unknown Type) - a programming/definition error, not a runtime condition
// failure.
var ErrInvalidRule = errors.New("invalid rule definition")

// ErrConditionEvaluation means a condition leaf could not be evaluated
// against the given EvalContext at runtime (a referenced field is missing,
// a cross-workflow instance doesn't exist, a value has the wrong shape for
// the requested comparison). Evaluate returns this wrapped with more detail;
// EvaluateRules treats it as this batch's fail-closed contract requires:
// log to the audit trail, skip the rule's actions, never abort the
// triggering CreateInstance/ExecuteTransition call.
var ErrConditionEvaluation = errors.New("rule condition could not be evaluated")

// ErrRuleNotFound means no workflow_rules row with the given ID is
// visible in the caller's firm context (RLS: either it doesn't exist, or
// it belongs to a different firm), or it doesn't belong to the given
// definitionKey - both resolve to the same error, by the same "don't leak
// existence" design every other *NotFound sentinel in this package
// follows (see engine.go's ErrDefinitionNotFound/ErrInstanceNotFound).
var ErrRuleNotFound = errors.New("workflow rule not found")

// EvalContext is everything Evaluate needs to check one rule's condition
// tree against one triggering instance: its own payload/state, and (for a
// cross-workflow "state" leaf) a pool to read another instance's state
// from within an RLS-scoped transaction.
type EvalContext struct {
	FirmID  uuid.UUID
	State   string
	Payload map[string]any
	// Pool is only required when ConditionTree contains a "state" leaf -
	// evaluating any other tree works with Pool left nil. Evaluation runs
	// synchronously (Vision §3: "no goroutines, no eventual consistency"),
	// and deliberately opens its own short-lived, firm-scoped transaction
	// against Pool (via zdb.WithFirmContext) rather than requiring the
	// caller's own already-open transaction - see EvaluateRules' doc
	// comment for why this batch's wiring runs after the triggering
	// write's own transaction has already committed.
	Pool *pgxpool.Pool
}

// Evaluate is the rule engine's pure entry point: does tree hold against
// ec? "Pure" here means it has no side effects of its own - a "state" leaf
// still performs a read (via ec.Pool), but Evaluate never writes anything;
// EvaluateRules below is the impure driver that acts on the result.
func Evaluate(ctx context.Context, tree ExpressionNode, ec EvalContext) (bool, error) {
	if tree.isLogicNode() {
		return evaluateLogicNode(ctx, tree, ec)
	}
	switch tree.Type {
	case ConditionField:
		return evaluateFieldCondition(tree, ec)
	case ConditionState:
		return evaluateStateCondition(ctx, tree, ec)
	case ConditionFieldCompare:
		return evaluateFieldCompareCondition(tree, ec)
	case ConditionEdgeEvent:
		return evaluateEdgeEventCondition(ctx, tree, ec)
	default:
		return false, fmt.Errorf("%w: unknown condition leaf type %q", ErrInvalidRule, tree.Type)
	}
}

func evaluateLogicNode(ctx context.Context, node ExpressionNode, ec EvalContext) (bool, error) {
	switch LogicOp(node.Op) {
	case LogicAnd:
		if len(node.Children) == 0 {
			return false, fmt.Errorf("%w: \"and\" node must have at least one child", ErrInvalidRule)
		}
		for _, child := range node.Children {
			ok, err := Evaluate(ctx, child, ec)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil

	case LogicOr:
		if len(node.Children) == 0 {
			return false, fmt.Errorf("%w: \"or\" node must have at least one child", ErrInvalidRule)
		}
		for _, child := range node.Children {
			ok, err := Evaluate(ctx, child, ec)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil

	case LogicNot:
		if len(node.Children) != 1 {
			return false, fmt.Errorf("%w: \"not\" node must have exactly one child", ErrInvalidRule)
		}
		ok, err := Evaluate(ctx, node.Children[0], ec)
		if err != nil {
			return false, err
		}
		return !ok, nil

	default:
		return false, fmt.Errorf("%w: unknown logic operator %q", ErrInvalidRule, node.Op)
	}
}

// evaluateFieldCondition implements the "field" leaf: compares
// ec.Payload[node.Field] against node.Value using node.Op.
func evaluateFieldCondition(node ExpressionNode, ec EvalContext) (bool, error) {
	if node.Field == "" {
		return false, fmt.Errorf("%w: \"field\" condition must set field", ErrInvalidRule)
	}
	actual, present := ec.Payload[node.Field]
	if !present {
		return false, fmt.Errorf("%w: field %q not present in instance payload", ErrConditionEvaluation, node.Field)
	}
	return compareValues(CompareOp(node.Op), actual, node.Value)
}

// evaluateFieldCompareCondition implements the "field_compare" leaf:
// compares ec.Payload[node.FieldA] against ec.Payload[node.FieldB], both
// read from the same instance's own payload.
func evaluateFieldCompareCondition(node ExpressionNode, ec EvalContext) (bool, error) {
	if node.FieldA == "" || node.FieldB == "" {
		return false, fmt.Errorf("%w: \"field_compare\" condition must set fieldA and fieldB", ErrInvalidRule)
	}
	a, aOK := ec.Payload[node.FieldA]
	b, bOK := ec.Payload[node.FieldB]
	if !aOK || !bOK {
		return false, fmt.Errorf("%w: field_compare requires both %q and %q present in instance payload", ErrConditionEvaluation, node.FieldA, node.FieldB)
	}
	return compareValues(CompareOp(node.Op), a, b)
}

// evaluateStateCondition implements the "state" leaf, the cross-workflow
// condition: node.InstanceIDField names a field on THIS instance's payload
// (expected to be a reference-type field, per the design brief) that
// points at another instance, belonging to the workflow definition named
// by node.DefinitionKey, within the same firm; the condition holds if that
// instance's current state equals node.State.
func evaluateStateCondition(ctx context.Context, node ExpressionNode, ec EvalContext) (bool, error) {
	if node.DefinitionKey == "" || node.InstanceIDField == "" || node.State == "" {
		return false, fmt.Errorf("%w: \"state\" condition must set definitionKey, instanceIdField, and state", ErrInvalidRule)
	}
	if ec.Pool == nil {
		return false, fmt.Errorf("%w: \"state\" condition requires a database pool", ErrConditionEvaluation)
	}

	raw, present := ec.Payload[node.InstanceIDField]
	if !present {
		return false, fmt.Errorf("%w: instance id field %q not present in instance payload", ErrConditionEvaluation, node.InstanceIDField)
	}
	refStr, ok := raw.(string)
	if !ok {
		return false, fmt.Errorf("%w: instance id field %q must be a string instance ID", ErrConditionEvaluation, node.InstanceIDField)
	}
	refID, err := uuid.Parse(refStr)
	if err != nil {
		return false, fmt.Errorf("%w: instance id field %q is not a valid instance ID: %s", ErrConditionEvaluation, node.InstanceIDField, err)
	}

	var stateKey string
	err = zdb.WithFirmContext(ctx, ec.Pool, ec.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT ws.key
			FROM workflow_instances wi
			JOIN workflow_states ws ON ws.id = wi.current_state_id
			JOIN workflow_definitions wd ON wd.id = wi.workflow_definition_id
			WHERE wi.id = $1 AND wi.firm_id = $2 AND wd.key = $3
		`, refID, ec.FirmID, node.DefinitionKey).Scan(&stateKey)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("%w: referenced %q instance %s not found in this firm", ErrConditionEvaluation, node.DefinitionKey, refID)
	}
	if err != nil {
		return false, fmt.Errorf("%w: read referenced instance state: %v", ErrConditionEvaluation, err)
	}
	return stateKey == node.State, nil
}

// evaluateEdgeEventCondition implements the "edge_event" leaf: true when
// an edge_events row exists for (firm, node.AgentID, node.EventType)
// with received_at within the last node.EdgeEventWithinMinutes minutes.
// Reads via ec.Pool (like evaluateStateCondition's own cross-workflow
// read), NOT ec.Payload - this leaf answers "has this happened
// recently", a question about the database's own history, not about the
// currently-evaluating instance/event's payload.
func evaluateEdgeEventCondition(ctx context.Context, node ExpressionNode, ec EvalContext) (bool, error) {
	if node.AgentID == "" || node.EventType == "" || node.EdgeEventWithinMinutes <= 0 {
		return false, fmt.Errorf("%w: \"edge_event\" condition must set agentId, eventType, and a positive withinMinutes", ErrInvalidRule)
	}
	if ec.Pool == nil {
		return false, fmt.Errorf("%w: \"edge_event\" condition requires a database pool", ErrConditionEvaluation)
	}
	agentID, err := uuid.Parse(node.AgentID)
	if err != nil {
		return false, fmt.Errorf("%w: \"edge_event\" condition's agentId %q is not a valid agent ID: %s", ErrConditionEvaluation, node.AgentID, err)
	}

	// Built as a Go string (e.g. "5 minutes") and cast directly to
	// ::interval, rather than concatenating a bound int parameter with a
	// literal inside the query - pgx's extended query protocol plans each
	// placeholder's wire encoding from the describe response before the
	// query executes, and an int value can't be encoded against a `text`-
	// typed placeholder that way even with an explicit SQL-side ::text
	// cast. EdgeEventWithinMinutes is validated positive above, so this
	// is never influenced by anything resembling free-form input.
	lookback := fmt.Sprintf("%d minutes", node.EdgeEventWithinMinutes)

	var exists bool
	err = zdb.WithFirmContext(ctx, ec.Pool, ec.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM edge_events
				WHERE firm_id = $1 AND agent_id = $2 AND event_type = $3
					AND received_at >= now() - $4::interval
			)
		`, ec.FirmID, agentID, node.EventType, lookback).Scan(&exists)
	})
	if err != nil {
		return false, fmt.Errorf("%w: check recent edge events: %v", ErrConditionEvaluation, err)
	}
	return exists, nil
}

// toFloat64 mirrors checkScalarValue's (engine.go) FieldTypeNumber switch:
// a JSON-decoded payload value is float64, but a Go caller (this package's
// own tests) may hand in any numeric kind directly.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}

// valuesEqual compares a and b for "eq"/"neq": numerically if both are
// numeric (so a JSON-decoded float64(5) equals a Go int(5)), otherwise by
// plain Go equality (covers string/bool; anything else - e.g. comparing a
// map - is simply never equal, not a type error).
func valuesEqual(a, b any) bool {
	af, aOK := toFloat64(a)
	bf, bOK := toFloat64(b)
	if aOK && bOK {
		return af == bf
	}
	return a == b
}

// compareValues implements every CompareOp both "field" and
// "field_compare" leaves use.
func compareValues(op CompareOp, a, b any) (bool, error) {
	switch op {
	case OpEq:
		return valuesEqual(a, b), nil
	case OpNeq:
		return !valuesEqual(a, b), nil
	case OpContains:
		as, aOK := a.(string)
		bs, bOK := b.(string)
		if !aOK || !bOK {
			return false, fmt.Errorf("%w: \"contains\" requires string values", ErrConditionEvaluation)
		}
		return strings.Contains(as, bs), nil
	case OpLt, OpGt, OpLte, OpGte:
		af, aOK := toFloat64(a)
		bf, bOK := toFloat64(b)
		if !aOK || !bOK {
			return false, fmt.Errorf("%w: %q requires numeric values", ErrConditionEvaluation, op)
		}
		switch op {
		case OpLt:
			return af < bf, nil
		case OpGt:
			return af > bf, nil
		case OpLte:
			return af <= bf, nil
		default:
			return af >= bf, nil
		}
	default:
		return false, fmt.Errorf("%w: unknown comparison operator %q", ErrInvalidRule, op)
	}
}

// ValidateExpressionTree checks tree is structurally sound before it's
// ever stored - a pure, DB-free counterpart to Evaluate's own runtime
// switch (this file, above): every logic node has the right number of
// children for its operator, every condition leaf has its required
// fields set and a known comparison operator. What this function
// deliberately CANNOT check, for the same reason DefinitionSpec.Validate's
// own doc comment gives for FieldTypeReference (spec.go): a "state" leaf's
// DefinitionKey isn't verified against a real workflow_definitions row
// here (no DB context) - that's the HTTP-reachable creation/update path's
// job (handlers.go's rule endpoints), which does have one.
func ValidateExpressionTree(tree ExpressionNode) error {
	if tree.isLogicNode() {
		switch LogicOp(tree.Op) {
		case LogicAnd, LogicOr:
			if len(tree.Children) == 0 {
				return fmt.Errorf("%w: %q node must have at least one child", ErrInvalidRule, tree.Op)
			}
		case LogicNot:
			if len(tree.Children) != 1 {
				return fmt.Errorf("%w: \"not\" node must have exactly one child", ErrInvalidRule)
			}
		default:
			return fmt.Errorf("%w: unknown logic operator %q", ErrInvalidRule, tree.Op)
		}
		for _, child := range tree.Children {
			if err := ValidateExpressionTree(child); err != nil {
				return err
			}
		}
		return nil
	}

	switch tree.Type {
	case ConditionField:
		if tree.Field == "" {
			return fmt.Errorf("%w: \"field\" condition must set field", ErrInvalidRule)
		}
		if !isKnownCompareOp(CompareOp(tree.Op), true) {
			return fmt.Errorf("%w: \"field\" condition has unknown operator %q", ErrInvalidRule, tree.Op)
		}
	case ConditionState:
		if tree.DefinitionKey == "" || tree.InstanceIDField == "" || tree.State == "" {
			return fmt.Errorf("%w: \"state\" condition must set definitionKey, instanceIdField, and state", ErrInvalidRule)
		}
	case ConditionFieldCompare:
		if tree.FieldA == "" || tree.FieldB == "" {
			return fmt.Errorf("%w: \"field_compare\" condition must set fieldA and fieldB", ErrInvalidRule)
		}
		if !isKnownCompareOp(CompareOp(tree.Op), false) {
			return fmt.Errorf("%w: \"field_compare\" condition has unknown operator %q", ErrInvalidRule, tree.Op)
		}
	case ConditionEdgeEvent:
		if tree.AgentID == "" || tree.EventType == "" || tree.EdgeEventWithinMinutes <= 0 {
			return fmt.Errorf("%w: \"edge_event\" condition must set agentId, eventType, and a positive withinMinutes", ErrInvalidRule)
		}
		if _, err := uuid.Parse(tree.AgentID); err != nil {
			return fmt.Errorf("%w: \"edge_event\" condition's agentId must be a valid UUID", ErrInvalidRule)
		}
	default:
		return fmt.Errorf("%w: unknown condition leaf type %q", ErrInvalidRule, tree.Type)
	}
	return nil
}

// isKnownCompareOp reports whether op is one ValidateExpressionTree/
// compareValues actually implements - allowContains is false for
// "field_compare" (OpContains is only meaningful for "field", per
// CompareOp's own doc comment).
func isKnownCompareOp(op CompareOp, allowContains bool) bool {
	switch op {
	case OpEq, OpNeq, OpLt, OpGt, OpLte, OpGte:
		return true
	case OpContains:
		return allowContains
	default:
		return false
	}
}

// ValidateActions checks Actions is structurally sound before it's ever
// stored or run - every action has a known Type and its type-specific
// required fields set. Mirrors DefinitionSpec.Validate's "reject before
// writing" discipline (spec.go).
func ValidateActions(actions []Action) error {
	for i, a := range actions {
		switch a.Type {
		case ActionTransition:
			if a.ActionKey == "" {
				return fmt.Errorf("%w: action %d: transition action must set actionKey", ErrInvalidRule, i)
			}
		case ActionNotify:
			if a.Channel != NotifyChannelAuditLog {
				return fmt.Errorf("%w: action %d: notify action's channel must be %q", ErrInvalidRule, i, NotifyChannelAuditLog)
			}
			if a.MessageTemplate == "" {
				return fmt.Errorf("%w: action %d: notify action must set messageTemplate", ErrInvalidRule, i)
			}
		case ActionSetField:
			if a.Field == "" {
				return fmt.Errorf("%w: action %d: set_field action must set field", ErrInvalidRule, i)
			}
		default:
			return fmt.Errorf("%w: action %d: unknown action type %q", ErrInvalidRule, i, a.Type)
		}
	}
	return nil
}

// renderMessageTemplate substitutes every "{{key}}" placeholder in
// template with payload[key] (formatted via fmt.Sprint), for whatever keys
// payload actually has - a deliberately simple, non-Turing-complete
// substitution (no expressions, no control flow), not a general templating
// engine: the input is a firm-authored string, not code, and this keeps it
// that way.
func renderMessageTemplate(template string, payload map[string]any) string {
	rendered := template
	for key, value := range payload {
		rendered = strings.ReplaceAll(rendered, "{{"+key+"}}", fmt.Sprint(value))
	}
	return rendered
}

// ruleRow is Rule's jsonb wire shape for the workflow_rules table -
// separate from Rule itself the same way payloadFieldRow (engine.go) stays
// separate from FieldSpec.
type ruleRow struct {
	ConditionTree json.RawMessage
	Actions       json.RawMessage
}

// validateRuleShape runs every pure, DB-free structural check a rule must
// pass before it's ever written: Name/DefinitionKey non-empty, a known
// Trigger, ValidateExpressionTree on ConditionTree, ValidateActions on
// Actions. Shared by CreateRule (the fixture/test entry point) and
// createRuleTx/updateRuleTx (the HTTP-reachable path, which additionally
// runs checkObviousSelfReferentialLoop - a real DB query, not something
// this pure function can do).
func validateRuleShape(rule Rule) error {
	if rule.Name == "" {
		return fmt.Errorf("%w: rule name must not be empty", ErrInvalidRule)
	}
	if rule.DefinitionKey == "" {
		return fmt.Errorf("%w: rule definitionKey must not be empty", ErrInvalidRule)
	}
	if rule.Trigger != TriggerOnCreate && rule.Trigger != TriggerOnTransition && rule.Trigger != TriggerSchedule && rule.Trigger != TriggerOnEdgeEvent {
		return fmt.Errorf("%w: rule trigger must be %q, %q, %q, or %q", ErrInvalidRule, TriggerOnCreate, TriggerOnTransition, TriggerSchedule, TriggerOnEdgeEvent)
	}

	if rule.Trigger == TriggerSchedule {
		// Partial implementation (scheduler.go's own doc comment): no
		// condition tree, notify-only actions - both enforced here, at
		// definition time, rather than left to fail confusingly at
		// runtime with no instance/payload context to evaluate against.
		if !isEmptyExpressionTree(rule.ConditionTree) {
			return fmt.Errorf("%w: schedule-triggered rules do not support condition trees yet", ErrInvalidRule)
		}
		for i, a := range rule.Actions {
			if a.Type != ActionNotify {
				return fmt.Errorf("%w: action %d: schedule-triggered rules only support %q actions", ErrInvalidRule, i, ActionNotify)
			}
		}
		if rule.ScheduleInterval == nil || strings.TrimSpace(*rule.ScheduleInterval) == "" {
			return fmt.Errorf("%w: schedule-triggered rules require scheduleInterval", ErrInvalidRule)
		}
		if d, err := time.ParseDuration(*rule.ScheduleInterval); err != nil || d <= 0 {
			return fmt.Errorf("%w: scheduleInterval must be a positive Go duration string (e.g. \"1h\", \"24h\")", ErrInvalidRule)
		}
	}

	if rule.Trigger == TriggerOnEdgeEvent {
		// Same reasoning as TriggerSchedule's own restriction just above,
		// for the same underlying cause: an edge event has no backing
		// workflow_instances row, so ActionTransition/ActionSetField have
		// nothing to act on - only ActionNotify is supported. Unlike
		// TriggerSchedule, a condition tree IS meaningful here (it's
		// evaluated against the incoming event's own payload, see
		// EvaluateEdgeEventRules), so it is NOT restricted to empty.
		for i, a := range rule.Actions {
			if a.Type != ActionNotify {
				return fmt.Errorf("%w: action %d: on_edge_event-triggered rules only support %q actions", ErrInvalidRule, i, ActionNotify)
			}
		}
	}

	// Schedule-triggered rules' condition tree is required to be the empty
	// placeholder above (isEmptyExpressionTree) - ValidateExpressionTree
	// itself would reject that same empty value for every other trigger
	// (an empty logic-node Op is not a recognized operator), so it's
	// skipped entirely here rather than taught a schedule-specific carve-out.
	if rule.Trigger != TriggerSchedule {
		if err := ValidateExpressionTree(rule.ConditionTree); err != nil {
			return err
		}
	}
	return ValidateActions(rule.Actions)
}

// isEmptyExpressionTree reports whether tree is the zero value - "no
// condition tree was set" - as opposed to a structurally trivial but
// non-zero one. Schedule-triggered rules require this today (see
// validateRuleShape); every other trigger runs ValidateExpressionTree's
// own full structural check regardless.
func isEmptyExpressionTree(tree ExpressionNode) bool {
	return tree.Op == "" && tree.Type == "" && len(tree.Children) == 0
}

// createRuleTx inserts rule as a new workflow_rules row within an
// already-open, firm-scoped transaction, returning its generated ID and
// created_at - the shared core both CreateRule (below) and
// CreateRuleForFirm (the HTTP-reachable path) build on, mirroring
// DefineWorkflow/DefineWorkflowTx's own split (engine.go). Callers are
// responsible for validation (validateRuleShape, plus
// checkObviousSelfReferentialLoop for the HTTP path) before calling this.
func createRuleTx(ctx context.Context, tx pgx.Tx, rule Rule) (uuid.UUID, time.Time, error) {
	conditionTreeJSON, err := json.Marshal(rule.ConditionTree)
	if err != nil {
		return uuid.UUID{}, time.Time{}, fmt.Errorf("marshal condition tree: %w", err)
	}
	actionsJSON, err := json.Marshal(rule.Actions)
	if err != nil {
		return uuid.UUID{}, time.Time{}, fmt.Errorf("marshal actions: %w", err)
	}

	var id uuid.UUID
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `
		INSERT INTO workflow_rules (firm_id, definition_key, name, trigger, condition_tree, actions, autonomous, enabled, schedule_interval)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at
	`, rule.FirmID, rule.DefinitionKey, rule.Name, string(rule.Trigger), conditionTreeJSON, actionsJSON, rule.Autonomous, rule.Enabled, rule.ScheduleInterval).Scan(&id, &createdAt); err != nil {
		return uuid.UUID{}, time.Time{}, fmt.Errorf("insert workflow rule: %w", err)
	}

	// A schedule-triggered rule needs an initial scheduled_rule_runs row
	// to ever run at all - nothing else seeds one (validateRuleShape has
	// already guaranteed ScheduleInterval parses as a positive duration
	// at this point). Scheduled one interval out, not immediately: a rule
	// shouldn't fire the instant it's saved.
	if rule.Trigger == TriggerSchedule {
		interval, err := time.ParseDuration(*rule.ScheduleInterval)
		if err != nil {
			return uuid.UUID{}, time.Time{}, fmt.Errorf("parse schedule interval: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO scheduled_rule_runs (firm_id, rule_id, scheduled_for)
			VALUES ($1, $2, now() + $3::interval)
		`, rule.FirmID, id, interval.String()); err != nil {
			return uuid.UUID{}, time.Time{}, fmt.Errorf("schedule initial run: %w", err)
		}
	}
	return id, createdAt, nil
}

// CreateRule validates and stores rule as a new workflow_rules row - the
// fixture/test entry point (see rules_integration_test.go). Not owner-
// gated and takes no acting userID: unlike CreateRuleForFirm (the HTTP-
// reachable path below), there is no caller-supplied firmID to validate
// against a caller's own membership here.
//
// ciaudit:ignore-firmid-check: provisioning-only helper, only called by
// test fixtures - never exposed via an HTTP handler with a caller-supplied
// firmID; see CreateRuleForFirm for the gated, HTTP-reachable counterpart.
func CreateRule(ctx context.Context, pool *pgxpool.Pool, rule Rule) (uuid.UUID, error) {
	if err := validateRuleShape(rule); err != nil {
		return uuid.UUID{}, err
	}

	var id uuid.UUID
	err := zdb.WithFirmContext(ctx, pool, rule.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, _, err = createRuleTx(ctx, tx, rule)
		return err
	})
	if err != nil {
		return uuid.UUID{}, err
	}
	return id, nil
}

// CreateRuleTx is CreateRule's already-open-transaction counterpart, for
// callers (internal/portability's configuration import) that need rule
// creation as one step inside a larger atomic operation - the same "*Tx,
// no authorization of its own, shared by every entry point" pattern
// DefineWorkflowTx already establishes.
//
// ciaudit:ignore-firmid-check: shared rule-creation primitive; every
// caller has already run its own authorization check before calling this.
func CreateRuleTx(ctx context.Context, tx pgx.Tx, rule Rule) (Rule, error) {
	if err := validateRuleShape(rule); err != nil {
		return Rule{}, err
	}
	id, createdAt, err := createRuleTx(ctx, tx, rule)
	if err != nil {
		return Rule{}, err
	}
	rule.ID = id
	rule.CreatedAt = createdAt
	return rule, nil
}

// RuleExistsTx reports whether firmID already has a rule with this exact
// (definitionKey, name) pair - workflow_rules carries no DB-level unique
// constraint on that pair (a rule has no other natural identity the way
// a workflow's own key, an account's code, or a product's SKU do), so
// configuration import's own idempotent "skip if exists" dedup for rules
// is an explicit application-level check against this, rather than
// catching a UNIQUE-violation error the schema doesn't raise.
//
// ciaudit:ignore-firmid-check: internal cross-package helper, called only
// by internal/portability's import path after its own owner-gate check
// has already run - firmID here scopes the query (defense in depth
// alongside RLS), it is not itself an authorization decision.
func RuleExistsTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, definitionKey, name string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM workflow_rules WHERE firm_id = $1 AND definition_key = $2 AND name = $3)
	`, firmID, definitionKey, name).Scan(&exists); err != nil {
		return false, fmt.Errorf("check rule exists: %w", err)
	}
	return exists, nil
}

// definitionExistsTx reports whether firmID has a workflow_definitions row
// keyed by definitionKey - the existence check CreateRuleForFirm runs
// before creating a rule against it, so a rule can't be created against a
// definition key that doesn't (yet, or ever) exist for this firm, giving
// a clean ErrDefinitionNotFound instead of surfacing the composite foreign
// key violation workflow_rules already enforces at the database level
// (migrations/0008_workflow_rules.up.sql).
//
// ciaudit:ignore-firmid-check: internal helper called only by
// CreateRuleForFirm, which has already run permission.IsMember/IsOwner
// before reaching this call - firmID here scopes an existence-check
// query, not a fresh authorization decision.
func definitionExistsTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, definitionKey string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM workflow_definitions WHERE firm_id = $1 AND key = $2)
	`, firmID, definitionKey).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check definition exists: %w", err)
	}
	return exists, nil
}

// checkObviousSelfReferentialLoop rejects the narrow, statically-
// detectable case the design brief asks for: a rule whose own action
// would immediately make it eligible to re-fire itself - Trigger
// on_transition plus a "transition" action whose ActionKey resolves (for
// this firm/definitionKey) to a transition that is a literal self-loop
// (from_state_id == to_state_id). EvaluateRules' on_transition trigger
// isn't scoped to which specific transition just fired - ANY transition on
// this definition re-evaluates every enabled on_transition rule, including
// this one - so a self-loop transition action guarantees the same rule
// becomes eligible again on its own action's own completion.
//
// Deliberately NOT the full arbitrary graph: a longer cycle through
// several different rules and transitions is not caught here - that
// would need real runtime cycle detection, which this batch doesn't add.
// The actual backstop for that broader case is EvaluateRules' existing
// fail-closed behavior (rules.go): an error partway through a chain never
// corrupts state or crashes the triggering operation, it just stops that
// one rule's actions and logs it - a bounded failure mode, not a proof of
// termination, but the one this codebase actually has today.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// CreateRuleForFirm/UpdateRuleForFirm, both of which have already run
// permission.IsMember/IsOwner before reaching this call - firmID here
// scopes a structural read query, not a fresh authorization decision.
func checkObviousSelfReferentialLoop(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, definitionKey string, trigger Trigger, actions []Action) error {
	if trigger != TriggerOnTransition {
		return nil
	}
	for _, a := range actions {
		if a.Type != ActionTransition {
			continue
		}
		var fromStateID, toStateID uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT wt.from_state_id, wt.to_state_id
			FROM workflow_transitions wt
			JOIN workflow_definitions wd ON wd.id = wt.workflow_definition_id
			WHERE wd.firm_id = $1 AND wd.key = $2 AND wt.action_key = $3
			LIMIT 1
		`, firmID, definitionKey, a.ActionKey).Scan(&fromStateID, &toStateID)
		if errors.Is(err, pgx.ErrNoRows) {
			// An unknown action key isn't this check's job to catch - it
			// will fail at evaluation time (ErrConditionEvaluation-style
			// fail-closed) or is a separate validation concern.
			continue
		}
		if err != nil {
			return fmt.Errorf("check self-referential loop: %w", err)
		}
		if fromStateID == toStateID {
			return fmt.Errorf("%w: transition action %q is a self-loop (from and to the same state) on an on_transition-triggered rule - this rule would become eligible to fire itself again immediately after its own action runs", ErrInvalidRule, a.ActionKey)
		}
	}
	return nil
}

// ruleAuditChanges builds the audit_log.changes payload CreateRuleForFirm/
// UpdateRuleForFirm/DeleteRuleForFirm each write - shared shape so a
// reader of the audit trail sees the same fields for every rule mutation.
func ruleAuditChanges(rule Rule) map[string]any {
	return map[string]any{
		"name":       rule.Name,
		"trigger":    string(rule.Trigger),
		"autonomous": rule.Autonomous,
		"enabled":    rule.Enabled,
	}
}

// CreateRuleForFirm is CreateRule's HTTP-reachable, owner-gated
// counterpart (handlers.go's handleCreateRule) - the actual point of this
// batch: until now, creating a rule was only reachable from Go fixtures.
// Owner-gated (permission.IsMember then IsOwner, the same order every
// owner-gated function in this codebase checks - see DefineWorkflowForFirm,
// engine.go) - defining a rule is a structural, autonomous-behavior-
// introducing decision, the same tier as defining a workflow itself.
// definitionKey must resolve to a real workflow_definitions row for
// firmID (definitionExistsTx) before anything is validated further.
// Writes one audit_log entry on success.
func CreateRuleForFirm(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, definitionKey string, rule Rule) (Rule, error) {
	rule.FirmID = firmID
	rule.DefinitionKey = definitionKey

	var result Rule
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
		}

		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrPermissionDenied
		}

		exists, err := definitionExistsTx(ctx, tx, firmID, definitionKey)
		if err != nil {
			return err
		}
		if !exists {
			return ErrDefinitionNotFound
		}

		if err := validateRuleShape(rule); err != nil {
			return err
		}
		if err := checkObviousSelfReferentialLoop(ctx, tx, firmID, definitionKey, rule.Trigger, rule.Actions); err != nil {
			return err
		}

		id, createdAt, err := createRuleTx(ctx, tx, rule)
		if err != nil {
			return err
		}
		result = rule
		result.ID = id
		result.CreatedAt = createdAt

		return auditlog.Write(ctx, tx, firmID, userID, id, ruleEntityType, ruleCreateAction, ruleAuditChanges(result))
	})
	if err != nil {
		return Rule{}, err
	}
	return result, nil
}

// getRuleTx reads one workflow_rules row scoped to (ruleID, firmID,
// definitionKey) - UpdateRuleForFirm/DeleteRuleForFirm's shared lookup.
// Scoping by definitionKey too (not just ruleID/firmID) means a ruleID
// that's real but belongs to a different definition within the same firm
// resolves to the same ErrRuleNotFound as a ruleID that doesn't exist at
// all - no cross-definition existence leak, matching this package's
// existing not-found conventions.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// UpdateRuleForFirm/DeleteRuleForFirm, both of which have already run
// permission.IsMember/IsOwner before reaching this call - firmID here
// scopes a read query, not a fresh authorization decision.
func getRuleTx(ctx context.Context, tx pgx.Tx, firmID, ruleID uuid.UUID, definitionKey string) (Rule, error) {
	row := tx.QueryRow(ctx, `
		SELECT `+ruleColumns+`
		FROM workflow_rules
		WHERE id = $1 AND firm_id = $2 AND definition_key = $3
	`, ruleID, firmID, definitionKey)
	rule, err := scanRule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, ErrRuleNotFound
	}
	if err != nil {
		return Rule{}, err
	}
	return rule, nil
}

// RuleUpdate is UpdateRuleForFirm's partial-update shape: a nil field
// leaves that column unchanged, a non-nil field overwrites it - the same
// "*T means optional, nil means untouched" convention internal/firm.Update's
// UpdateFields already uses for firm metadata (internal/firm/firm.go).
type RuleUpdate struct {
	Name          *string
	Enabled       *bool
	Autonomous    *bool
	ConditionTree *ExpressionNode
	Actions       *[]Action
	// ScheduleInterval, when non-nil, replaces the rule's own field - same
	// "can set, can't explicitly clear back to unset" convention Name's
	// own *string already has in this struct (there is no way to PATCH a
	// rule's name back to empty either).
	ScheduleInterval *string
}

// UpdateRuleForFirm applies patch to ruleID (scoped to firmID/definitionKey
// via getRuleTx), re-validating the FULL resulting rule (not just the
// changed fields) before writing - the same "the whole thing must be valid
// after the patch, not just what changed" discipline as everywhere else
// mutation in this codebase. Owner-gated identically to CreateRuleForFirm.
func UpdateRuleForFirm(ctx context.Context, pool *pgxpool.Pool, firmID, userID, ruleID uuid.UUID, definitionKey string, patch RuleUpdate) (Rule, error) {
	var result Rule
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
		}

		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrPermissionDenied
		}

		existing, err := getRuleTx(ctx, tx, firmID, ruleID, definitionKey)
		if err != nil {
			return err
		}

		updated := existing
		if patch.Name != nil {
			updated.Name = *patch.Name
		}
		if patch.Enabled != nil {
			updated.Enabled = *patch.Enabled
		}
		if patch.Autonomous != nil {
			updated.Autonomous = *patch.Autonomous
		}
		if patch.ConditionTree != nil {
			updated.ConditionTree = *patch.ConditionTree
		}
		if patch.Actions != nil {
			updated.Actions = *patch.Actions
		}
		if patch.ScheduleInterval != nil {
			updated.ScheduleInterval = patch.ScheduleInterval
		}

		if err := validateRuleShape(updated); err != nil {
			return err
		}
		if err := checkObviousSelfReferentialLoop(ctx, tx, firmID, definitionKey, updated.Trigger, updated.Actions); err != nil {
			return err
		}

		conditionTreeJSON, err := json.Marshal(updated.ConditionTree)
		if err != nil {
			return fmt.Errorf("marshal condition tree: %w", err)
		}
		actionsJSON, err := json.Marshal(updated.Actions)
		if err != nil {
			return fmt.Errorf("marshal actions: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE workflow_rules
			SET name = $1, condition_tree = $2, actions = $3, autonomous = $4, enabled = $5, schedule_interval = $6
			WHERE id = $7
		`, updated.Name, conditionTreeJSON, actionsJSON, updated.Autonomous, updated.Enabled, updated.ScheduleInterval, ruleID); err != nil {
			return fmt.Errorf("update workflow rule: %w", err)
		}

		result = updated
		return auditlog.Write(ctx, tx, firmID, userID, ruleID, ruleEntityType, ruleUpdateAction, ruleAuditChanges(result))
	})
	if err != nil {
		return Rule{}, err
	}
	return result, nil
}

// DeleteRuleForFirm deletes ruleID (scoped to firmID/definitionKey via
// getRuleTx). Owner-gated identically to CreateRuleForFirm/UpdateRuleForFirm.
func DeleteRuleForFirm(ctx context.Context, pool *pgxpool.Pool, firmID, userID, ruleID uuid.UUID, definitionKey string) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
		}

		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrPermissionDenied
		}

		existing, err := getRuleTx(ctx, tx, firmID, ruleID, definitionKey)
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `DELETE FROM workflow_rules WHERE id = $1`, ruleID); err != nil {
			return fmt.Errorf("delete workflow rule: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, ruleID, ruleEntityType, ruleDeleteAction, ruleAuditChanges(existing))
	})
}

// ruleColumns is the column list every workflow_rules SELECT in this file
// shares - one definition instead of five independently-drifting copies,
// now that schedule_interval (this batch) is a column too.
const ruleColumns = "id, firm_id, definition_key, name, trigger, condition_tree, actions, autonomous, enabled, schedule_interval, created_at"

// scanRule reads one workflow_rules row (ruleColumns' shape) into a Rule.
func scanRule(row pgx.Row) (Rule, error) {
	var r Rule
	var trigger string
	var rr ruleRow
	if err := row.Scan(&r.ID, &r.FirmID, &r.DefinitionKey, &r.Name, &trigger, &rr.ConditionTree, &rr.Actions, &r.Autonomous, &r.Enabled, &r.ScheduleInterval, &r.CreatedAt); err != nil {
		return Rule{}, err
	}
	r.Trigger = Trigger(trigger)
	if err := json.Unmarshal(rr.ConditionTree, &r.ConditionTree); err != nil {
		return Rule{}, fmt.Errorf("unmarshal condition tree: %w", err)
	}
	if err := json.Unmarshal(rr.Actions, &r.Actions); err != nil {
		return Rule{}, fmt.Errorf("unmarshal actions: %w", err)
	}
	return r, nil
}

// ListRulesForDefinition returns every workflow_rules row for
// definitionKey within firmID, most recently created first - the read
// side of the "minimal read-only listing endpoint" the design brief asks
// for, so a rule IS visible in the UI (e.g. on the workflow definition
// page) even though the rule-creation form is deferred to a later batch.
// userID must actually belong to firmID (permission.IsMember) - same
// discipline every other firm-scoped read in this package follows; see
// CurrentState's doc comment (engine.go) for why RLS scoping alone isn't
// enough.
func ListRulesForDefinition(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, definitionKey string) ([]Rule, error) {
	var rules []Rule
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrDefinitionNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT `+ruleColumns+`
			FROM workflow_rules
			WHERE definition_key = $1
			ORDER BY created_at DESC
		`, definitionKey)
		if err != nil {
			return fmt.Errorf("list workflow rules: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			r, err := scanRule(rows)
			if err != nil {
				return err
			}
			rules = append(rules, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return rules, nil
}

// listEnabledRulesTx loads every enabled workflow_rules row matching
// firmID/definitionKey/trigger, for EvaluateRules' own internal use - no
// membership check here (EvaluateRules runs as part of the triggering
// operation itself, which has already been authorized).
//
// ciaudit:ignore-firmid-check: internal helper called only by
// EvaluateRules, itself only reachable from CreateInstance/
// ExecuteTransition after those have already run permission.IsMember/Has -
// firmID here scopes a read query (RLS is the actual enforcement, same
// defense-in-depth reasoning this package's other internal helpers use),
// not a fresh authorization decision of its own.
func listEnabledRulesTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, definitionKey string, trigger Trigger) ([]Rule, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+ruleColumns+`
		FROM workflow_rules
		WHERE firm_id = $1 AND definition_key = $2 AND trigger = $3 AND enabled
		ORDER BY created_at
	`, firmID, definitionKey, string(trigger))
	if err != nil {
		return nil, fmt.Errorf("list enabled workflow rules: %w", err)
	}
	defer rows.Close()
	var rules []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// listEnabledEdgeEventRulesTx loads every enabled on_edge_event rule for
// firmID, across ALL definition keys - deliberately NOT filtered by one
// definitionKey the way listEnabledRulesTx is for on_create/
// on_transition. An edge_events row has no natural workflow definition
// of its own: internal/edgeagent.RecordEvent is reached purely via an
// edge agent's bearer token, with no workflow context at all - there is
// no single instance/definition an incoming event could be said to
// belong to. workflow_rules.definition_key stays NOT NULL regardless
// (a rule must still be filed under some existing definition to be
// created at all, unchanged), but that definitionKey plays no role in
// on_edge_event dispatch - a deliberate deviation from on_create/
// on_transition's own dispatch shape, worth calling out explicitly
// (see this batch's own design write-up).
//
// ciaudit:ignore-firmid-check: internal helper called only by
// EvaluateEdgeEventRules, itself only reachable from
// internal/edgeagent.RecordEvent after the edge agent's own bearer
// token has already been authenticated - firmID here scopes a read
// query (RLS is the actual enforcement), not a fresh authorization
// decision of its own.
func listEnabledEdgeEventRulesTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID) ([]Rule, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+ruleColumns+`
		FROM workflow_rules
		WHERE firm_id = $1 AND trigger = $2 AND enabled
		ORDER BY created_at
	`, firmID, string(TriggerOnEdgeEvent))
	if err != nil {
		return nil, fmt.Errorf("list enabled on_edge_event workflow rules: %w", err)
	}
	defer rows.Close()
	var rules []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// EvaluateEdgeEventRules is on_edge_event's own driver - the trigger-
// specific counterpart EvaluateRules is for on_create/on_transition,
// kept as a SEPARATE function rather than folded into EvaluateRules
// because an edge event has no backing workflow_instances row at all:
// EvaluateRules' own dispatch (executeRuleActions, firstTransitionAction/
// createPendingApprovalAndNotify) is built entirely around acting on one
// specific instanceID, which doesn't exist here. validateRuleShape
// already restricts on_edge_event rules to ActionNotify-only actions at
// definition time (mirroring TriggerSchedule's own restriction, for the
// identical reason: no instance/payload-backed target to transition or
// set a field on) - so this driver only ever needs to run that one
// action kind.
//
// Like scheduler.go's own runScheduledRule, a matched rule's notify
// action is logged via slog rather than written to audit_log:
// audit_log.user_id is NOT NULL (references a real users row), and
// there is no real ZonaryOS user in scope for an automated edge-event
// match the way there is for a human-triggered CreateInstance/
// ExecuteTransition call - the edge agent's bearer token resolves to an
// AuthenticatedAgent (internal/edgeagent), not a users.id. This also
// means the Autonomous/human-approval toggle (Never-Violate Rule 8) is
// not consulted here, the same way runScheduledRule doesn't consult it
// either: the toggle exists to gate STATE-CHANGING actions
// (ActionTransition) behind human approval before they run, and
// ActionNotify - the only action kind reachable here - changes no
// state, so there is nothing for approval to meaningfully gate.
//
// Called from internal/edgeagent's own HTTP handler, after RecordEvent's
// insert has already committed - the same post-commit seam EvaluateRules
// itself uses for on_create/on_transition (see its own doc comment for
// why), and the same "error is logged by the caller, never propagated
// back to fail the triggering write" contract.
//
// ciaudit:ignore-firmid-check: called only by
// internal/edgeagent.RecordEvent, itself only reachable once the edge
// agent's own bearer token has already been authenticated - firmID here
// scopes which firm's rules run (RLS is the actual enforcement), it is
// not itself a fresh authorization decision, the same reasoning
// listEnabledEdgeEventRulesTx's own suppression documents just above.
func EvaluateEdgeEventRules(ctx context.Context, pool *pgxpool.Pool, firmID, agentID uuid.UUID, eventType string, payload map[string]any) error {
	var rules []Rule
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		rules, err = listEnabledEdgeEventRulesTx(ctx, tx, firmID)
		return err
	})
	if err != nil {
		return fmt.Errorf("load on_edge_event workflow rules: %w", err)
	}

	ec := EvalContext{FirmID: firmID, Payload: payload, Pool: pool}
	for _, rule := range rules {
		matched, evalErr := Evaluate(ctx, rule.ConditionTree, ec)
		if evalErr != nil {
			slog.Warn("on_edge_event rule evaluation failed", "firmId", firmID, "agentId", agentID, "ruleId", rule.ID, "ruleName", rule.Name, "err", evalErr)
			continue
		}
		if !matched {
			continue
		}
		for _, action := range rule.Actions {
			// validateRuleShape already guarantees every action on an
			// on_edge_event rule is ActionNotify - see that function's
			// own doc comment.
			message := renderMessageTemplate(action.MessageTemplate, payload)
			slog.Info("edge event rule fired", "firmId", firmID, "agentId", agentID, "eventType", eventType, "ruleId", rule.ID, "ruleName", rule.Name, "message", message)
		}
	}
	return nil
}

// ruleEntityType/ruleEvaluationErrorAction/rulePendingApprovalAction are
// the audit_log.entity_type/action values EvaluateRules writes under - kept
// distinct from workflow_instance's own action vocabulary ("create", an
// actionKey, "view") so a reader of the log can tell "the rule engine did
// this" apart from a human/API-driven change.
const (
	ruleEntityType            = "workflow_rule"
	ruleEvaluationErrorAction = "evaluation_error"
	rulePendingApprovalAction = "pending_approval"
	ruleNotifyAction          = "notify"
	ruleSetFieldAction        = "set_field"
	// ruleCreateAction/ruleUpdateAction/ruleDeleteAction are the
	// audit_log.action values CreateRuleForFirm/UpdateRuleForFirm/
	// DeleteRuleForFirm write under - the human/API-driven counterpart to
	// EvaluateRules' own action vocabulary above.
	ruleCreateAction = "create"
	ruleUpdateAction = "update"
	ruleDeleteAction = "delete"
	// workflowInstanceEntityType mirrors the literal 'workflow_instance'
	// string engine.go's own audit_log writes use directly in SQL - named
	// here since this file's rule-action writers (ActionNotify/
	// ActionSetField) go through auditlog.Write's Go API instead of a raw
	// INSERT, and need the same string as a Go value.
	workflowInstanceEntityType = "workflow_instance"
)

// EvaluateRules is the rule engine's impure driver: load every enabled
// rule for firmID/definitionKey/trigger, evaluate each rule's
// ConditionTree against the triggering instance, and run matching rules'
// Actions in order.
//
// Called at the end of CreateInstance and ExecuteTransition, AFTER the
// triggering write has already committed (see both functions' call
// sites) - not inside the same transaction. The design brief calls for
// synchronous, inline evaluation ("no goroutines, no eventual
// consistency"), which this still is: the caller does not return to its
// own caller until EvaluateRules itself returns. Running after commit
// (rather than nested inside the triggering transaction) is what the
// brief's own wording anticipates ("inside the same transaction where
// possible, or immediately after commit where transaction boundaries
// prevent it") - CreateInstance/ExecuteTransition's transaction is
// entirely internal to zdb.WithFirmContext, with no seam for a caller to
// inject extra work before it commits, so "immediately after commit" is
// the seam actually available. A rule's own actions (ActionTransition,
// ActionSetField) still each get their own real transaction when they run.
//
// Fail-closed (per the design brief): if a rule's ConditionTree returns an
// error (a missing field, an unresolvable cross-workflow reference, ...),
// that rule's Actions are NOT executed - the error is written to audit_log
// instead, and evaluation moves on to the next rule. An error evaluating
// or acting on one rule never surfaces back to the CreateInstance/
// ExecuteTransition caller - the triggering operation has already
// committed by the time this runs, so there is nothing left for a returned
// error to roll back; EvaluateRules only returns an error for a genuine
// infrastructure failure (e.g. it couldn't even list the firm's rules),
// which callers log rather than treat as the triggering operation having
// failed.
//
// ciaudit:ignore-firmid-check: called only by CreateInstance/
// ExecuteTransition, both of which have already run permission.IsMember/Has
// for the operation this evaluates rules on - firmID here scopes which
// firm's rules run, it is not itself a fresh authorization decision.
func EvaluateRules(ctx context.Context, pool *pgxpool.Pool, firmID, userID, instanceID uuid.UUID, definitionKey string, trigger Trigger, state string, payload map[string]any) error {
	var rules []Rule
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		rules, err = listEnabledRulesTx(ctx, tx, firmID, definitionKey, trigger)
		return err
	})
	if err != nil {
		return fmt.Errorf("load workflow rules: %w", err)
	}

	ec := EvalContext{FirmID: firmID, State: state, Payload: payload, Pool: pool}
	for _, rule := range rules {
		matched, evalErr := Evaluate(ctx, rule.ConditionTree, ec)
		if evalErr != nil {
			writeRuleAuditEntry(ctx, pool, firmID, userID, instanceID, rule, ruleEvaluationErrorAction, map[string]any{
				"ruleName": rule.Name,
				"error":    evalErr.Error(),
			})
			continue
		}
		if !matched {
			continue
		}

		if !rule.Autonomous {
			// Open Points item 41: a non-autonomous match now creates a
			// real pending_approvals row plus notifications (approvals.go)
			// when the rule proposes a transition - not just this audit_log
			// entry, which stays as the permanent record that the match
			// happened, unconditionally, the same as before this batch.
			// See firstTransitionAction's own doc comment for why only the
			// first transition action, if any, becomes an approval - a
			// "notify"/"set_field"-only rule has no permission-gated action
			// to seek approval for, so it keeps the old audit-only behavior.
			if action, ok := firstTransitionAction(rule.Actions); ok {
				if _, approvalErr := createPendingApprovalAndNotify(ctx, pool, firmID, userID, instanceID, definitionKey, state, rule, action.ActionKey); approvalErr != nil {
					writeRuleAuditEntry(ctx, pool, firmID, userID, instanceID, rule, ruleEvaluationErrorAction, map[string]any{
						"ruleName": rule.Name,
						"error":    approvalErr.Error(),
					})
					continue
				}
			}
			writeRuleAuditEntry(ctx, pool, firmID, userID, instanceID, rule, rulePendingApprovalAction, map[string]any{
				"ruleName": rule.Name,
			})
			continue
		}

		if actErr := executeRuleActions(ctx, pool, firmID, userID, instanceID, rule, payload); actErr != nil {
			writeRuleAuditEntry(ctx, pool, firmID, userID, instanceID, rule, ruleEvaluationErrorAction, map[string]any{
				"ruleName": rule.Name,
				"error":    actErr.Error(),
			})
		}
	}
	return nil
}

// writeRuleAuditEntry records one audit_log row about the rule engine's own
// behavior (an evaluation error, a pending-approval match, ...), in its
// own short transaction - best-effort: a failure to write the audit entry
// itself is swallowed (nothing left to roll back or report to), matching
// this function's callers, which are themselves already inside
// EvaluateRules' fail-closed, never-abort-the-caller contract.
//
// ciaudit:ignore-firmid-check: internal helper called only from within
// EvaluateRules' own already-authorized flow (see its doc comment).
func writeRuleAuditEntry(ctx context.Context, pool *pgxpool.Pool, firmID, userID, instanceID uuid.UUID, rule Rule, action string, changes map[string]any) {
	changes["instanceId"] = instanceID.String()
	_ = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return auditlog.Write(ctx, tx, firmID, userID, rule.ID, ruleEntityType, action, changes)
	})
}

// executeRuleActions runs rule's Actions, in order, against instanceID.
//
// ciaudit:ignore-firmid-check: internal helper called only from within
// EvaluateRules' own already-authorized flow (see its doc comment); the
// transition action it can trigger goes through the real, fully-checked
// ExecuteTransition, not a bypass of it.
func executeRuleActions(ctx context.Context, pool *pgxpool.Pool, firmID, userID, instanceID uuid.UUID, rule Rule, payload map[string]any) error {
	for _, action := range rule.Actions {
		switch action.Type {
		case ActionTransition:
			if err := ExecuteTransition(ctx, pool, firmID, userID, instanceID, action.ActionKey, nil); err != nil {
				return fmt.Errorf("rule %q transition action %q: %w", rule.Name, action.ActionKey, err)
			}

		case ActionNotify:
			message := renderMessageTemplate(action.MessageTemplate, payload)
			err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
				return auditlog.Write(ctx, tx, firmID, userID, instanceID, workflowInstanceEntityType, ruleNotifyAction, map[string]any{
					"ruleId":   rule.ID.String(),
					"ruleName": rule.Name,
					"message":  message,
				})
			})
			if err != nil {
				return fmt.Errorf("rule %q notify action: %w", rule.Name, err)
			}

		case ActionSetField:
			if err := setInstanceField(ctx, pool, firmID, userID, instanceID, rule, action); err != nil {
				return fmt.Errorf("rule %q set_field action: %w", rule.Name, err)
			}

		default:
			return fmt.Errorf("%w: unknown action type %q", ErrInvalidRule, action.Type)
		}
	}
	return nil
}

// setInstanceField implements ActionSetField: merges {field: value} into
// instanceID's stored payload, the same jsonb `||` merge ExecuteTransition
// uses for a caller-supplied payload, and records one audit_log row.
//
// ciaudit:ignore-firmid-check: internal helper called only from within
// EvaluateRules' own already-authorized flow (see its doc comment).
func setInstanceField(ctx context.Context, pool *pgxpool.Pool, firmID, userID, instanceID uuid.UUID, rule Rule, action Action) error {
	patch, err := json.Marshal(map[string]any{action.Field: action.Value})
	if err != nil {
		return fmt.Errorf("marshal set_field patch: %w", err)
	}
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE workflow_instances
			SET payload = payload || $1::jsonb, updated_at = now()
			WHERE id = $2
		`, patch, instanceID); err != nil {
			return fmt.Errorf("update workflow instance payload: %w", err)
		}
		return auditlog.Write(ctx, tx, firmID, userID, instanceID, workflowInstanceEntityType, ruleSetFieldAction, map[string]any{
			"ruleId":   rule.ID.String(),
			"ruleName": rule.Name,
			"field":    action.Field,
			"value":    action.Value,
		})
	})
}
