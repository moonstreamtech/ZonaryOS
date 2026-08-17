// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai

import "context"

// customProvider is a stub for provider='custom' - reserved for a firm
// running its own OpenAI-API-compatible endpoint (self-hosted model, a
// gateway, etc.). Not implemented this batch: there is no agreed request/
// response shape or endpoint-configuration field (ai_configurations.settings
// jsonb exists precisely so a future batch can add e.g. a "baseURL" key
// without a migration) - see googleProvider's own doc comment for the same
// "exposed now, implemented later" reasoning.
type customProvider struct {
	model  string
	apiKey string
}

func newCustomProvider(model, apiKey string) *customProvider {
	return &customProvider{model: model, apiKey: apiKey}
}

func (p *customProvider) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	return "", ErrProviderNotImplemented
}
