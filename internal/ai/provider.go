// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai

import "context"

// ProviderName is the set of AI providers ai_configurations.provider
// accepts (mirrors the migration's own CHECK constraint).
type ProviderName string

const (
	ProviderAnthropic ProviderName = "anthropic"
	ProviderOpenAI    ProviderName = "openai"
	ProviderGoogle    ProviderName = "google"
	ProviderCustom    ProviderName = "custom"
)

// Provider is the thin abstraction every concrete AI backend implements.
// Adding a new provider is a new file implementing this interface plus one
// new case in NewProvider - not a scattered if/else chain across
// suggest_workflow.go/suggest_report.go/anomaly.go, which only ever talk
// to this interface.
//
// Complete sends a single system+user prompt pair and returns the
// provider's raw text response. No streaming, no multi-turn conversation,
// no tool use - every caller in this package is a single request/response
// exchange (Part 2/3's own "no auto-retry, one attempt" contract, and
// Part 4's single daily anomaly-check call).
type Provider interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// NewProvider builds the concrete Provider for name, using the given model
// and (already-decrypted) apiKey.
//
// Fully implemented this batch: Anthropic, OpenAI - both simple REST
// callers, no vendor SDK dependency (matching this codebase's general
// preference for direct HTTP calls over adding SDK dependencies for a
// single endpoint). Stubbed this batch: Google, Custom - both return
// ErrProviderNotImplemented on every call, clearly documented in their own
// files; a firm may still select them in ai_configurations (the CHECK
// constraint allows it) but every /ai/* call against such a configuration
// fails with a clear error rather than silently doing nothing.
func NewProvider(name ProviderName, model, apiKey string) (Provider, error) {
	switch name {
	case ProviderAnthropic:
		return newAnthropicProvider(model, apiKey), nil
	case ProviderOpenAI:
		return newOpenAIProvider(model, apiKey), nil
	case ProviderGoogle:
		return newGoogleProvider(model, apiKey), nil
	case ProviderCustom:
		return newCustomProvider(model, apiKey), nil
	default:
		return nil, ErrInvalidConfig
	}
}

// ProviderFactory is the hook SuggestWorkflow/SuggestReport/the anomaly
// scheduler actually call to obtain a Provider - defaults to NewProvider.
// Tests substitute a mock implementation of the Provider interface via
// this var (package ai_test, an external test package, can reassign an
// exported var but not an unexported function) so none of this package's
// tests ever make a real network call to an AI vendor, per Part 5's own
// test requirement ("mock the AI provider (no real API calls in
// tests)"). Production code (cmd/server/main.go, and this package's own
// non-test files) never reassigns this - it is a test-only seam.
var ProviderFactory = NewProvider
