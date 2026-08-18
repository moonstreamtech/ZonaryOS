// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// mockTransitionHook records every event it's called with, and optionally
// returns a canned error - the same "mock the extension point, don't
// stand up a real plugin" approach internal/ai's own suggest-workflow
// tests use for their own external dependency (an AI provider there,
// a plugin here).
type mockTransitionHook struct {
	events []TransitionHookEvent
	err    error
}

func (m *mockTransitionHook) OnTransition(ctx context.Context, event TransitionHookEvent) error {
	m.events = append(m.events, event)
	return m.err
}

// resetTransitionHooksForTest clears the package-level transitionHooks
// slice before/after a test - transitionHooks is normally populated once
// at startup (cmd/server/main.go) and never touched again, but a test
// package running multiple tests against the same process needs a clean
// slate each time to avoid one test's registered hook leaking into
// another's assertions.
func resetTransitionHooksForTest(t *testing.T) {
	t.Helper()
	original := transitionHooks
	transitionHooks = nil
	t.Cleanup(func() { transitionHooks = original })
}

// TestDispatchTransitionHooks_CallsRegisteredHook is Part 2's own
// explicit test requirement: registering a WorkflowHook and confirming
// it's called after ExecuteTransition - exercised here at
// dispatchTransitionHooks' own level (the exact function
// ExecuteTransition calls post-commit) rather than through a full
// ExecuteTransition integration test, since dispatchTransitionHooks'
// own contract (call every hook, with this event) doesn't need a real
// Postgres instance or a real workflow instance to verify.
func TestDispatchTransitionHooks_CallsRegisteredHook(t *testing.T) {
	resetTransitionHooksForTest(t)
	hook := &mockTransitionHook{}
	RegisterTransitionHook(hook)

	event := TransitionHookEvent{
		FirmID: uuid.New(), InstanceID: uuid.New(), DefinitionKey: "stock_to_sale",
		FromState: "pending", ToState: "sold", ActionKey: "sell", Payload: map[string]any{"quantity": 5},
	}
	dispatchTransitionHooks(context.Background(), event)

	if len(hook.events) != 1 {
		t.Fatalf("expected the hook to be called exactly once, got %d calls", len(hook.events))
	}
	got := hook.events[0]
	if got.FirmID != event.FirmID || got.InstanceID != event.InstanceID || got.DefinitionKey != event.DefinitionKey ||
		got.FromState != event.FromState || got.ToState != event.ToState || got.ActionKey != event.ActionKey {
		t.Fatalf("expected the hook to receive the exact event passed to dispatchTransitionHooks, got %+v", got)
	}
}

// TestDispatchTransitionHooks_OneHookFailingDoesNotStopOthers proves the
// error-handling contract dispatchTransitionHooks' own doc comment
// describes: one hook's error is logged and does not stop the remaining
// registered hooks from running.
func TestDispatchTransitionHooks_OneHookFailingDoesNotStopOthers(t *testing.T) {
	resetTransitionHooksForTest(t)
	failing := &mockTransitionHook{err: errors.New("boom")}
	succeeding := &mockTransitionHook{}
	RegisterTransitionHook(failing)
	RegisterTransitionHook(succeeding)

	event := TransitionHookEvent{FirmID: uuid.New(), InstanceID: uuid.New(), ActionKey: "sell"}
	dispatchTransitionHooks(context.Background(), event)

	if len(failing.events) != 1 {
		t.Fatalf("expected the failing hook to still be called once, got %d calls", len(failing.events))
	}
	if len(succeeding.events) != 1 {
		t.Fatalf("expected the second hook to still be called despite the first hook's error, got %d calls", len(succeeding.events))
	}
}

func TestDispatchTransitionHooks_NoRegisteredHooksIsANoOp(t *testing.T) {
	resetTransitionHooksForTest(t)
	// Must not panic with zero registered hooks.
	dispatchTransitionHooks(context.Background(), TransitionHookEvent{})
}
