// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ratelimit

import (
	"net/http"
	"regexp"
	"strings"
)

// uuidSegmentPattern mirrors internal/apm.NormalizePathPattern's own
// UUID-segment matcher exactly - both packages need the identical
// "is this path segment a firm ID" shape check, and duplicating the
// three-line regex here is simpler and more honest than introducing a
// cross-package dependency for it (internal/ratelimit has no other
// reason to import internal/apm, and internal/apm has no reason to
// import internal/ratelimit).
var uuidSegmentPattern = regexp.MustCompile(`^(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// FirmKey extracts the firm-scoping key Middleware buckets a request's
// rate limit under - the UUID segment immediately following a "firms"
// path segment, same shape internal/apm.ExtractFirmID already
// establishes, or "unscoped" for any request with no firm in its own
// path (a handful of platform-admin/unauthenticated routes - these all
// share one bucket per category rather than each getting its own
// unlimited pass, so even an unauthenticated-route flood is still
// bounded, just not per-caller).
func FirmKey(path string) string {
	segments := strings.Split(path, "/")
	for i := 0; i < len(segments)-1; i++ {
		if segments[i] == "firms" && uuidSegmentPattern.MatchString(segments[i+1]) {
			return segments[i+1]
		}
	}
	return "unscoped"
}

// bulkMarker/aiSuggestMarker/analyticsIngestPath/exportSuffix are the
// exact path shapes this batch's own brief maps to each non-standard
// category - see the four routes each one matches, listed alongside its
// constant.
const (
	// bulkMarker matches POST .../invoices/bulk-status and
	// POST .../workflow-instances/bulk-transition.
	bulkMarker = "bulk"
	// aiSuggestMarker matches POST .../ai/suggest-workflow and
	// POST .../ai/suggest-report.
	aiSuggestMarker = "/ai/suggest"
	// exportSuffix matches GET .../export (internal/portability).
	exportSuffix = "/export"
	// analyticsIngestSuffix matches POST .../analytics/events
	// (internal/analytics.IngestEvents).
	analyticsIngestSuffix = "/analytics/events"
)

// Classify maps one request's method+path to its rate-limit Category -
// a plain path-shape check (the same reasoning
// internal/apm.NormalizePathPattern's own doc comment gives for why
// path shape, not a mux-matched pattern, is what's reliably observable
// from an outer middleware in this codebase's own chain), checked in a
// fixed order: bulk/AI/export/analytics-ingest are all more specific
// than "standard", so each is checked before falling through to the
// default.
func Classify(r *http.Request) Category {
	path := r.URL.Path

	if strings.Contains(path, bulkMarker) {
		return CategoryBulk
	}
	if strings.Contains(path, aiSuggestMarker) {
		return CategoryAI
	}
	if r.Method == http.MethodPost && strings.HasSuffix(path, analyticsIngestSuffix) {
		return CategoryAnalyticsIngest
	}
	if r.Method == http.MethodGet && strings.HasSuffix(path, exportSuffix) {
		return CategoryExport
	}
	return CategoryStandard
}
