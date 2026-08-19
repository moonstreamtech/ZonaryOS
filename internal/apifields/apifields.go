// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package apifields is the mobile-optimized-API batch's shared, dependency
// -free leaf package for two concerns every major list endpoint
// (products, customers, invoices, workflow instances) needs the same
// answer to: field projection (?fields=) and delta sync (?updated_since=).
//
// Field projection uses an ALLOWLIST, not reflection: Project takes the
// caller's own closed map[string]bool of the response DTO's real JSON
// field names (the same "caller supplies a hardcoded allow-list, an
// unrecognized name is rejected rather than silently ignored" discipline
// internal/queryfilter.BuildClause and internal/reports.entityRegistry
// already establish for filters/report fields) - reflecting over the
// response struct's own json tags at request time would let a field
// added to a struct in some later, unrelated change become projectable
// without anyone deciding it should be; an explicit allowlist keeps that
// a deliberate per-endpoint choice.
package apifields

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrUnknownField means a ?fields= entry isn't in the caller's own
// allowlist - a 400, not a silently-dropped field.
var ErrUnknownField = errors.New("unknown field")

// ParseFields splits a comma-separated ?fields= value, trimming
// whitespace and dropping empty entries. An empty/absent raw value
// returns nil, meaning "no projection - full response".
func ParseFields(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	fields := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			fields = append(fields, p)
		}
	}
	return fields
}

// ParseUpdatedSince parses a ?updated_since= value as RFC3339. An empty
// raw value returns (nil, nil) - "no delta filter".
func ParseUpdatedSince(raw string) (*time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("updated_since must be RFC3339 (e.g. 2026-08-01T00:00:00Z): %w", err)
	}
	return &t, nil
}

// Project marshals v (typically a []someResponse or a single
// someResponse) to JSON, then, if fields is non-empty, filters every
// object in the result down to just those keys. Every name in fields
// must be present in allowed (the caller's own DTO field allowlist) or
// this returns ErrUnknownField wrapping the bad name - the caller should
// map that to a 400. len(fields)==0 returns v marshaled unchanged (no
// projection requested).
func Project(v any, fields []string, allowed map[string]bool) (json.RawMessage, error) {
	if len(fields) == 0 {
		return json.Marshal(v)
	}
	for _, f := range fields {
		if !allowed[f] {
			return nil, fmt.Errorf("%w: %q", ErrUnknownField, f)
		}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal response: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode response for projection: %w", err)
	}
	return json.Marshal(projectValue(decoded, fields))
}

func projectValue(v any, fields []string) any {
	switch val := v.(type) {
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = projectValue(item, fields)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(fields))
		for _, f := range fields {
			if raw, ok := val[f]; ok {
				out[f] = raw
			}
		}
		return out
	default:
		return v
	}
}
