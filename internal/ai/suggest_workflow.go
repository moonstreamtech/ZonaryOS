// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

const suggestWorkflowAuditAction = "suggest_workflow"

// workflowSuggestionPermission is workflowSuggestionState/Transition's own
// wire shape for the `permission` object - identical JSON tags to
// internal/workflow/handlers.go's own definePermissionRequest, so the
// response this endpoint returns is byte-for-byte postable to
// POST /api/firms/{firmID}/workflow-definitions without any client-side
// reshaping.
type workflowSuggestionPermission struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

type workflowSuggestionState struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	IsInitial  bool   `json:"isInitial"`
	IsTerminal bool   `json:"isTerminal"`
}

// workflowSuggestionTransition deliberately has NO journal/stockAdjustment/
// delivery/customer/invoice fields (defineTransitionRequest's own optional
// Effects wire fields) - a scope-narrowing decision for this batch: an AI
// suggestion only ever scaffolds states/transitions/fields, never
// financial/inventory/logistics effect templates, since those require
// real domain knowledge (actual chart-of-accounts codes, actual stock
// reasons) the AI cannot safely infer from a one-paragraph description.
// The system prompt below explicitly instructs the model not to include
// them; if it does anyway, json.Unmarshal into this struct silently drops
// the unknown fields (Go's default decode-into-known-fields-only
// behavior), so a hallucinated effect can never reach DefineWorkflowForFirm.
type workflowSuggestionTransition struct {
	FromStateKey string                       `json:"fromStateKey"`
	ToStateKey   string                       `json:"toStateKey"`
	ActionKey    string                       `json:"actionKey"`
	Name         string                       `json:"name"`
	Permission   workflowSuggestionPermission `json:"permission"`
}

type workflowSuggestionField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

// WorkflowSuggestion is SuggestWorkflow's response shape - a 1:1 subset of
// internal/workflow/handlers.go's own defineWorkflowRequest (same JSON
// tags, effects omitted per this file's own doc comment above), so a
// caller can POST this struct's JSON verbatim to
// /api/firms/{firmID}/workflow-definitions.
type WorkflowSuggestion struct {
	Key              string                         `json:"key"`
	Name             string                         `json:"name"`
	CreatePermission workflowSuggestionPermission   `json:"createPermission"`
	States           []workflowSuggestionState      `json:"states"`
	Transitions      []workflowSuggestionTransition `json:"transitions"`
	Fields           []workflowSuggestionField      `json:"fields,omitempty"`
}

const workflowSuggestionSystemPrompt = `You design business-process workflows for an ERP/WMS/MES system. Given a plain-language description of a business process, respond with ONLY a single JSON object (no markdown code fences, no commentary before or after) matching exactly this shape:

{
  "key": "snake_case_unique_identifier",
  "name": "Human Readable Name",
  "createPermission": {"key": "permission.key.for.creating.instances", "description": "..."},
  "states": [
    {"key": "state_key", "name": "State Name", "isInitial": true, "isTerminal": false}
  ],
  "transitions": [
    {"fromStateKey": "state_a", "toStateKey": "state_b", "actionKey": "action_key", "name": "Action Name", "permission": {"key": "permission.key.for.this.action", "description": "..."}}
  ],
  "fields": [
    {"name": "fieldName", "type": "string", "required": true}
  ]
}

Rules:
- Exactly one state must have "isInitial": true.
- Every transition's fromStateKey/toStateKey must reference a state key defined in "states".
- "fields" is optional; valid "type" values are: string, number, boolean, date, enum, reference, array.
- Do NOT include any "journal", "stockAdjustment", "delivery", "customer", or "invoice" keys anywhere in the response - those require real chart-of-accounts/inventory/logistics configuration you do not have access to and must be added later by a human reviewing this suggestion.
- Respond with ONLY the JSON object, nothing else.

Example, for "when a customer places an order, hold it until payment is confirmed, then fulfill it":
{"key":"order_to_fulfillment","name":"Order to Fulfillment","createPermission":{"key":"orders.create","description":"Create a new order"},"states":[{"key":"pending_payment","name":"Pending Payment","isInitial":true,"isTerminal":false},{"key":"paid","name":"Paid","isInitial":false,"isTerminal":false},{"key":"fulfilled","name":"Fulfilled","isInitial":false,"isTerminal":true}],"transitions":[{"fromStateKey":"pending_payment","toStateKey":"paid","actionKey":"confirm_payment","name":"Confirm Payment","permission":{"key":"orders.confirm_payment","description":"Confirm payment was received"}},{"fromStateKey":"paid","toStateKey":"fulfilled","actionKey":"fulfill","name":"Fulfill Order","permission":{"key":"orders.fulfill","description":"Mark the order fulfilled"}}],"fields":[{"name":"customerName","type":"string","required":true}]}`

// SuggestWorkflow calls the firm's active AI provider with description and
// returns a WorkflowSuggestion the caller can review, edit, then POST to
// /workflow-definitions. Owner-gated (same tier as defining a workflow
// directly). Returns ErrNoActiveConfig (mapped to 404, Part 5's opt-in
// contract) if the firm has no active AI configuration.
//
// No PII/business data ever reaches the AI provider here: description is
// the caller's OWN free-text input about a process they want to build, not
// any stored business record - see anomaly.go's own doc comment for where
// the PII-firewall requirement actually bites (aggregating real journal
// data).
//
// No auto-retry: exactly one provider call. On invalid JSON or a spec that
// fails DefinitionSpec.Validate, returns ErrInvalidAISuggestion wrapping
// the raw provider response text, so the caller can surface it for human
// review rather than silently retrying (Part 2's own contract).
func SuggestWorkflow(ctx context.Context, pool *pgxpool.Pool, encryptor *Encryptor, firmID, userID uuid.UUID, description string) (WorkflowSuggestion, error) {
	description = strings.TrimSpace(description)
	if description == "" {
		return WorkflowSuggestion{}, fmt.Errorf("%w: description must not be empty", ErrInvalidConfig)
	}

	var (
		cfg       Configuration
		plaintext string
	)
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		cfg, plaintext, err = getActiveConfigTx(ctx, tx, encryptor, firmID)
		return err
	})
	if err != nil {
		return WorkflowSuggestion{}, err
	}

	provider, err := ProviderFactory(cfg.Provider, cfg.Model, plaintext)
	if err != nil {
		return WorkflowSuggestion{}, err
	}

	raw, err := provider.Complete(ctx, workflowSuggestionSystemPrompt, description)
	if err != nil {
		return WorkflowSuggestion{}, err
	}

	suggestion, spec, parseErr := parseWorkflowSuggestion(raw)
	auditErr := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return auditlog.Write(ctx, tx, firmID, userID, cfg.ID, configAuditEntityType, suggestWorkflowAuditAction, map[string]any{
			"source":   "ai",
			"provider": string(cfg.Provider),
			"model":    cfg.Model,
			"valid":    parseErr == nil,
		})
	})
	if auditErr != nil {
		return WorkflowSuggestion{}, fmt.Errorf("audit AI workflow suggestion: %w", auditErr)
	}
	if parseErr != nil {
		return WorkflowSuggestion{}, fmt.Errorf("%w: %s", ErrInvalidAISuggestion, raw)
	}
	if err := spec.Validate(); err != nil {
		return WorkflowSuggestion{}, fmt.Errorf("%w: %s", ErrInvalidAISuggestion, raw)
	}

	return suggestion, nil
}

// parseWorkflowSuggestion decodes raw (the AI provider's raw text
// response) as a WorkflowSuggestion and converts it to a
// workflow.DefinitionSpec for structural validation - the same
// wire-struct-to-domain-struct conversion internal/workflow/handlers.go's
// own handleDefineWorkflow performs on the request side, minus the effects
// fields this endpoint never generates (see WorkflowSuggestion's own doc
// comment).
func parseWorkflowSuggestion(raw string) (WorkflowSuggestion, workflow.DefinitionSpec, error) {
	var s WorkflowSuggestion
	if err := json.Unmarshal([]byte(extractJSONObject(raw)), &s); err != nil {
		return WorkflowSuggestion{}, workflow.DefinitionSpec{}, err
	}

	spec := workflow.DefinitionSpec{
		Key:  s.Key,
		Name: s.Name,
		CreatePermission: workflow.PermissionSpec{
			Key: s.CreatePermission.Key, Description: s.CreatePermission.Description,
		},
		States:      make([]workflow.StateSpec, 0, len(s.States)),
		Transitions: make([]workflow.TransitionSpec, 0, len(s.Transitions)),
	}
	for _, st := range s.States {
		spec.States = append(spec.States, workflow.StateSpec{
			Key: st.Key, Name: st.Name, IsInitial: st.IsInitial, IsTerminal: st.IsTerminal,
		})
	}
	for _, t := range s.Transitions {
		spec.Transitions = append(spec.Transitions, workflow.TransitionSpec{
			FromStateKey: t.FromStateKey, ToStateKey: t.ToStateKey, ActionKey: t.ActionKey, Name: t.Name,
			Permission: workflow.PermissionSpec{Key: t.Permission.Key, Description: t.Permission.Description},
		})
	}
	if len(s.Fields) > 0 {
		spec.Fields = make([]workflow.FieldSpec, 0, len(s.Fields))
		for _, f := range s.Fields {
			spec.Fields = append(spec.Fields, workflow.FieldSpec{
				Name: f.Name, Type: workflow.FieldType(f.Type), Required: f.Required,
			})
		}
	}
	return s, spec, nil
}

// extractJSONObject trims anything before the first '{' and after the
// last '}' in s - a defensive allowance for a provider that ignores the
// system prompt's "ONLY the JSON object" instruction and wraps its
// response in a markdown code fence or a leading sentence. Not a JSON
// repair tool: if what remains still isn't valid JSON, json.Unmarshal
// simply fails and the caller surfaces ErrInvalidAISuggestion with the
// original raw text, exactly Part 2's "no auto-retry" contract.
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end == -1 || end < start {
		return s
	}
	return s[start : end+1]
}
