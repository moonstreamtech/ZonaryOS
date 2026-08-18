// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// anthropicAPIURL is the Anthropic Messages API endpoint. Not
// configurable - a firm changing endpoints entirely is exactly what the
// "custom" provider (custom.go) is for.
const anthropicAPIURL = "https://api.anthropic.com/v1/messages"

// anthropicAPIVersion is the required "anthropic-version" request header.
// Pinned, not derived from anything user-configurable - Anthropic's own
// API versioning contract.
const anthropicAPIVersion = "2023-06-01"

// anthropicMaxTokens bounds every completion this package requests. The
// callers in this package (suggest_workflow.go/suggest_report.go/
// anomaly.go) all expect a single bounded JSON or short-text response,
// never a long-form document - 4096 is comfortably enough for a
// DefinitionSpec/QuerySpec JSON body or a one-paragraph anomaly summary.
const anthropicMaxTokens = 4096

// anthropicTimeout bounds a single Complete call - generous enough for a
// real model response, but callers (especially the daily anomaly
// scheduler) must not hang indefinitely on a slow/unreachable provider.
const anthropicTimeout = 60 * time.Second

type anthropicProvider struct {
	model  string
	apiKey string
	client *http.Client
}

func newAnthropicProvider(model, apiKey string) *anthropicProvider {
	return &anthropicProvider{
		model:  model,
		apiKey: apiKey,
		client: &http.Client{Timeout: anthropicTimeout},
	}
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Complete implements Provider by calling the Anthropic Messages API with
// a single user-turn message and the given system prompt.
func (p *anthropicProvider) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	reqBody := anthropicRequest{
		Model:     p.model,
		MaxTokens: anthropicMaxTokens,
		System:    systemPrompt,
		Messages: []anthropicMessage{
			{Role: "user", Content: userPrompt},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("%w: marshal request: %v", ErrProviderCallFailed, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("%w: build request: %v", ErrProviderCallFailed, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrProviderCallFailed, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: read response: %v", ErrProviderCallFailed, err)
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("%w: unparseable response body: %v", ErrProviderCallFailed, err)
	}

	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil {
			return "", fmt.Errorf("%w: anthropic API returned %d: %s", ErrProviderCallFailed, resp.StatusCode, parsed.Error.Message)
		}
		return "", fmt.Errorf("%w: anthropic API returned %d", ErrProviderCallFailed, resp.StatusCode)
	}

	for _, block := range parsed.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("%w: no text content block in anthropic response", ErrProviderCallFailed)
}
