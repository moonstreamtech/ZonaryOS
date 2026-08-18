// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Plugin/extension architecture + developer API/SDK foundation, Part 2's
// WorkflowHook wiring. TransitionHook/TransitionHookEvent/
// RegisterTransitionHook are defined HERE, in internal/workflow, rather
// than this package importing internal/plugin.WorkflowHook directly, to
// avoid an import cycle: internal/plugin imports internal/reports (for
// QuerySpec/KPIResult, its other two extension points), and
// internal/reports already imports internal/workflow (for
// KPIKindWorkflowStateCount) - so internal/workflow importing
// internal/plugin back would complete the cycle
// workflow -> plugin -> reports -> workflow.
//
// The resolution mirrors internal/reports.ExternalSource/
// ExternalKPIProvider's own (see that package's external.go doc
// comment): internal/plugin.TransitionEvent is a type ALIAS for
// TransitionHookEvent below (internal/plugin imports internal/workflow
// directly - fine, since this package never imports internal/plugin
// back), so internal/plugin.WorkflowHook's OnTransition method has a
// signature structurally identical to TransitionHook's below, and
// cmd/server/main.go can pass a plugin.WorkflowHook value anywhere a
// TransitionHook is expected with zero adapter code, Go's usual
// interface-to-interface assignability.
package workflow

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// TransitionHookEvent is what TransitionHook.OnTransition receives - one
// executed workflow transition, already committed to the database by the
// time any hook sees it (see ExecuteTransition's own call site for
// exactly when this fires, and dispatchTransitionHooks' own doc comment
// for the error-handling contract).
type TransitionHookEvent struct {
	FirmID        uuid.UUID
	InstanceID    uuid.UUID
	DefinitionKey string
	FromState     string
	ToState       string
	ActionKey     string
	Payload       map[string]any
}

// TransitionHook is called after a workflow transition executes and
// commits - see internal/plugin.WorkflowHook's own doc comment for the
// trust/PII-firewall posture this batch takes for registered hooks.
type TransitionHook interface {
	OnTransition(ctx context.Context, event TransitionHookEvent) error
}

// transitionHooks is populated exactly once, at startup
// (cmd/server/main.go, before any request is served) - Part 2's own
// "immutable after init, no hot-reloading" scope boundary, the same
// no-mutex-needed reasoning internal/plugin.Registry's own doc comment
// gives for its own slices.
var transitionHooks []TransitionHook

// RegisterTransitionHook adds hook to the set ExecuteTransition dispatches
// to after every committed transition.
func RegisterTransitionHook(hook TransitionHook) {
	transitionHooks = append(transitionHooks, hook)
}

// dispatchTransitionHooks calls OnTransition on every registered
// TransitionHook, in registration order, for event - called from
// ExecuteTransition, synchronously, AFTER the transition's own
// transaction has already committed and the existing webhook.Dispatch/
// EvaluateRules calls have run.
//
// Sync, not async (unlike internal/webhook.Dispatch's own goroutine-per-
// delivery design for its outbound HTTP calls): a plugin hook is
// trusted, in-process Go code, not a network endpoint, so there's no
// latency/reliability reason to pay goroutine-management complexity for
// it - see internal/plugin.Registry.DispatchTransition's own doc comment
// for the full reasoning (this function is that design's actual
// call site).
//
// Error handling: one hook's error is logged (slog.Warn) and does not
// stop the remaining hooks, and never propagates to ExecuteTransition's
// own caller - the same post-commit, log-and-continue contract
// EvaluateRules' own call just above already follows, deliberately
// reused rather than inventing a different failure mode for plugins.
func dispatchTransitionHooks(ctx context.Context, event TransitionHookEvent) {
	for _, hook := range transitionHooks {
		if err := hook.OnTransition(ctx, event); err != nil {
			slog.Warn("plugin workflow hook failed", "firmId", event.FirmID, "instanceId", event.InstanceID, "actionKey", event.ActionKey, "err", err)
		}
	}
}
