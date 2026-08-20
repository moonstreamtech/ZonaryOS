// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apm

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// uuidSegmentPattern matches one path segment shaped like a UUID (any
// version/variant - this is a shape check, not a strict RFC 4122
// validator).
var uuidSegmentPattern = regexp.MustCompile(`^(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// NormalizePathPattern collapses a real request path (e.g.
// "/api/firms/3aa7de0c-.../products") into its own path PATTERN (e.g.
// "/api/firms/{id}/products") by replacing every UUID-shaped segment
// with a fixed placeholder - the grouping key request_metrics.path_pattern
// stores, so "p95 response time for the products list endpoint" is one
// row across every firm/product ID that ever hit it, not one row per
// distinct URL ever seen.
//
// This is a structural, path-shape-only transform - deliberately NOT a
// lookup against each module's own registered mux pattern (Go's
// net/http.Request does carry a matched Pattern field as of Go 1.23, but
// only on the exact *http.Request instance net/http.ServeMux itself
// matched; internal/platform/middleware.RequestTimeout/APIVersionAlias
// each create their own derived request via r.WithContext/r.Clone before
// the mux ever sees it, so an outer middleware like RequestID has no
// reliable way to read back the pattern the mux later matched inside a
// request object it no longer holds a reference to). Reconstructing the
// pattern from the path's own shape avoids that fragility entirely, at
// the cost of not distinguishing "products" from any other list endpoint
// whose path happens to have the same segment shape - an acceptable
// trade for this batch's own grouping need (Part 1's own design report
// documents this choice).
func NormalizePathPattern(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if uuidSegmentPattern.MatchString(seg) {
			segments[i] = "{id}"
		}
	}
	return strings.Join(segments, "/")
}

// ExtractFirmID returns the UUID segment immediately following a
// "firms" path segment, if the path has that shape (e.g.
// "/api/firms/{firmID}/products") - nil otherwise. This is the entire
// mechanism behind request_metrics.request_firm_id: no request context/
// identity lookup, just the same path-shape reasoning
// NormalizePathPattern already uses, applied to find which segment (if
// any) is the firm ID.
func ExtractFirmID(path string) *uuid.UUID {
	segments := strings.Split(path, "/")
	for i := 0; i < len(segments)-1; i++ {
		if segments[i] == "firms" && uuidSegmentPattern.MatchString(segments[i+1]) {
			id, err := uuid.Parse(segments[i+1])
			if err != nil {
				return nil
			}
			return &id
		}
	}
	return nil
}
