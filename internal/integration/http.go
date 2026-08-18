// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// httpTimeout bounds every outbound HTTP call this package makes
// directly (TestConnection's ping, ProcessInboundSyncs' own fetch) -
// outbound entity-sync pushes go through internal/webhook.SendSigned
// instead, which has its own identical-value deliveryTimeout constant;
// kept as two separate constants (not one shared exported one) since
// they belong to two different packages' own scope, the same way this
// codebase generally doesn't share small package-local constants across
// package boundaries.
const httpTimeout = 10 * time.Second

// maxInboundResponseBytes caps how much of an inbound connector's own
// response body ProcessInboundSyncs reads - a misbehaving or malicious
// source URL returning gigabytes of JSON must not be able to exhaust
// this process's memory. 10MB is generous for the kind of small,
// periodic entity-sync payloads this batch's own design brief describes
// (a products/customers feed, not a bulk data warehouse export).
const maxInboundResponseBytes = 10 << 20

// pingURL performs a plain GET against targetURL and reports whether it
// was reachable - "reachable" means the request got ANY HTTP response at
// all with a status under 500 (a webhook-style endpoint that only
// accepts POST commonly answers a GET with 404/405, which still proves
// the URL itself resolves and something is listening - a genuinely more
// useful signal for "is this connector configured correctly" than
// requiring a literal 2xx). A 5xx response, or no response at all
// (DNS failure, connection refused, timeout), is reported as
// unreachable.
func pingURL(ctx context.Context, targetURL string) error {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("build test request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxInboundResponseBytes))

	if resp.StatusCode >= 500 {
		return fmt.Errorf("connection test failed: target responded with status %d", resp.StatusCode)
	}
	return nil
}

// fetchJSONRecords performs a GET against sourceURL and decodes the
// response as either a single JSON object (returned as a one-element
// slice) or a JSON array of objects - ProcessInboundSyncs' own fetch
// step. A partner system reasonably might expose either shape ("give me
// today's products" as one array, vs. a single-record webhook-style
// endpoint), so both are accepted rather than forcing every connector's
// source to wrap a single record in an array.
func fetchJSONRecords(ctx context.Context, sourceURL string) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build fetch request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInboundResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch failed: source responded with status %d", resp.StatusCode)
	}

	var single map[string]any
	if err := json.Unmarshal(body, &single); err == nil {
		return []map[string]any{single}, nil
	}
	var multiple []map[string]any
	if err := json.Unmarshal(body, &multiple); err != nil {
		return nil, fmt.Errorf("response is not a JSON object or array of objects: %w", err)
	}
	return multiple, nil
}
