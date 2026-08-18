// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai

import "context"

// googleProvider is a stub. A firm may configure provider='google' (the
// migration's CHECK constraint allows it - the option is deliberately
// exposed now so a future batch only needs to implement Complete here,
// not add a new provider value or touch any caller), but every call
// fails clearly with ErrProviderNotImplemented rather than silently doing
// nothing or panicking.
//
// Scope decision for this batch: only Anthropic and OpenAI are
// implemented as real REST callers (anthropic.go/openai.go); Google
// (Gemini) is deferred, no Vision/Open Points text requires it in this
// batch.
type googleProvider struct {
	model  string
	apiKey string
}

func newGoogleProvider(model, apiKey string) *googleProvider {
	return &googleProvider{model: model, apiKey: apiKey}
}

func (p *googleProvider) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	return "", ErrProviderNotImplemented
}
