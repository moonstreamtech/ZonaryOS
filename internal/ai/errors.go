// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - every write in this package
	// (configuring AI, asking it a question) is owner-gated, the same
	// tier internal/edgeagent uses for registering an agent.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's AI configuration")

	// ErrConfigNotFound means no ai_configurations row with the given id
	// is visible in the caller's firm context.
	ErrConfigNotFound = errors.New("AI configuration not found")

	// ErrInvalidConfig means CreateConfig was given a structurally
	// invalid configuration (e.g. an unknown provider, an empty model,
	// an empty API key).
	ErrInvalidConfig = errors.New("invalid AI configuration")

	// ErrNoActiveConfig is the Part 5 safety-constraint sentinel: no
	// active ai_configurations row exists for the firm, so every
	// /ai/suggest-*/anomaly-relevant call must behave as if the feature
	// does not exist - mapped to 404, never 403 (see handlers.go's own
	// doc comment on why 404, not 403).
	ErrNoActiveConfig = errors.New("AI is not configured for this firm")

	// ErrEncryptionKeyNotSet means ZONARYOS_AI_ENCRYPTION_KEY is unset
	// (or malformed) at the point an API key needs to be encrypted or
	// decrypted - a real 500, not something callers can route around,
	// since without this key no AI configuration can ever be stored or
	// used at all.
	ErrEncryptionKeyNotSet = errors.New("AI encryption key is not configured (ZONARYOS_AI_ENCRYPTION_KEY)")

	// ErrInvalidAISuggestion means the configured AI provider's response
	// could not be parsed as valid JSON, or parsed but failed the
	// target spec's own structural validation (DefinitionSpec.Validate/
	// reports.BuildQuery) - surfaced as 422 with the raw AI response, so
	// a human can see exactly what the model returned (Part 2's own "no
	// auto-retry, one attempt, then human review" contract).
	ErrInvalidAISuggestion = errors.New("AI response was not a valid suggestion")

	// ErrProviderCallFailed wraps a genuine infrastructure/network
	// failure calling the configured AI provider (as opposed to the
	// provider responding with something unusable, which is
	// ErrInvalidAISuggestion instead).
	ErrProviderCallFailed = errors.New("AI provider call failed")

	// ErrProviderNotImplemented is returned by a stubbed Provider
	// implementation (see provider.go's own doc comment on which
	// providers are fully implemented in this batch vs. stubbed).
	ErrProviderNotImplemented = errors.New("this AI provider is not yet implemented")
)
