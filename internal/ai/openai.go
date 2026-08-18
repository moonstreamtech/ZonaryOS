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

// openAIAPIURL is the OpenAI Chat Completions endpoint.
const openAIAPIURL = "https://api.openai.com/v1/chat/completions"

// openAITimeout mirrors anthropicTimeout's own reasoning.
const openAITimeout = 60 * time.Second

type openAIProvider struct {
	model  string
	apiKey string
	client *http.Client
}

func newOpenAIProvider(model, apiKey string) *openAIProvider {
	return &openAIProvider{
		model:  model,
		apiKey: apiKey,
		client: &http.Client{Timeout: openAITimeout},
	}
}

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Complete implements Provider by calling the OpenAI Chat Completions API
// with a system message followed by a single user message.
func (p *openAIProvider) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	reqBody := openAIRequest{
		Model: p.model,
		Messages: []openAIMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("%w: marshal request: %v", ErrProviderCallFailed, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIAPIURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("%w: build request: %v", ErrProviderCallFailed, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrProviderCallFailed, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%w: read response: %v", ErrProviderCallFailed, err)
	}

	var parsed openAIResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("%w: unparseable response body: %v", ErrProviderCallFailed, err)
	}

	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil {
			return "", fmt.Errorf("%w: openai API returned %d: %s", ErrProviderCallFailed, resp.StatusCode, parsed.Error.Message)
		}
		return "", fmt.Errorf("%w: openai API returned %d", ErrProviderCallFailed, resp.StatusCode)
	}

	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("%w: no choices in openai response", ErrProviderCallFailed)
	}
	return parsed.Choices[0].Message.Content, nil
}
