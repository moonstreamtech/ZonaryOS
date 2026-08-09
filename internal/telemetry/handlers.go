// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package telemetry

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
)

// RegisterRoutes wires this package's two HTTP-reachable endpoints into
// mux:
//
//   - POST /telemetry/report - the "central server" side of the
//     reporting flow. Today, since Open Points item 34's real separate
//     central server doesn't exist yet, every installation both SENDS
//     to and (via this handler) RECEIVES its own reports - a
//     self-referential loop, consistent with
//     internal/discovery.HandleWellKnown answering with the
//     installation's own PublicURL. This handler intentionally does the
//     minimum a real central server's ingestion endpoint would need to
//     do at all: accept a well-formed Report body and acknowledge it.
//     It does NOT persist anything to Postgres (no new migration -
//     aggregate counters are a genuinely transient, log-only concern at
//     this stage, not data this codebase has a retention/access-control
//     story for yet - see the KVKK cross-reference this batch adds to
//     docs/OPEN_POINTS.md) - it logs a one-line summary (counts only,
//     already never-content by construction, see Report's doc comment)
//     and returns 202 Accepted.
//   - POST /telemetry/client-report - what the web frontend's own
//     batching client (web/src/lib/telemetry.ts) flushes to
//     periodically, forwarded here by the frontend's
//     /api/telemetry Next.js route handler. Body shape is
//     ClientReport (page-view and feature-usage counts only - see that
//     type's doc comment) and is merged straight into reporter's own
//     feature-event counters, so both server- and client-origin counts
//     ride the same periodic flush to the central address.
//
// Both routes are only ever registered when reporter is non-nil (i.e.
// ZONARYOS_TELEMETRY_ENABLED=true - see cmd/server/main.go's call site)
// - on a disabled installation, neither endpoint exists at all (a
// request to either path 404s exactly as it would if this package were
// never imported), matching this package's default-disabled guarantee:
// disabled means no counters, no background flush, AND no listening
// surface, not merely "the surface exists but silently no-ops."
func RegisterRoutes(mux *http.ServeMux, reporter *Reporter) {
	if reporter == nil {
		return
	}
	mux.Handle("POST /telemetry/report", handleReport())
	mux.Handle("POST /telemetry/client-report", handleClientReport(reporter))
}

// handleReport is the minimal "central server" ingestion stub described
// above - see RegisterRoutes's comment for why this logs rather than
// persists.
func handleReport() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var rep Report
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "failed to read report body", http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(body, &rep); err != nil {
			http.Error(w, "malformed report body", http.StatusBadRequest)
			return
		}
		slog.Info("telemetry: received report",
			"periodStart", rep.PeriodStart.Format("15:04:05"), "periodEnd", rep.PeriodEnd.Format("15:04:05"),
			"requestEndpoints", len(rep.RequestCounts), "sourceIPs", len(rep.SourceIPCounts), "featureEvents", len(rep.FeatureEventCounts))
		w.WriteHeader(http.StatusAccepted)
	}
}

// ClientReport is the batched shape web/src/lib/telemetry.ts sends -
// page/screen view counts (by route) and feature-usage counts (by named
// event, e.g. "workflow_instance_created") only. No page content, no
// request/response bodies, no business-data fields - see this package's
// doc comment for the absolute rule this mirrors.
type ClientReport struct {
	// PageViewCounts is route path -> view count (e.g.
	// "/[locale]/dashboard" -> 3) - which screens were opened and how
	// often, never what was rendered on them.
	PageViewCounts map[string]int `json:"pageViewCounts"`
	// FeatureEventCounts is named feature-success event -> count (e.g.
	// "workflow_instance_created" -> 2) - counts of actions the
	// frontend already knows succeeded from its own existing UI flow,
	// not new instrumentation reading business data.
	FeatureEventCounts map[string]int `json:"featureEventCounts"`
}

// handleClientReport merges an incoming ClientReport into reporter's
// feature-event counters, prefixing page-view counts so they're visibly
// distinguishable from server-recorded feature events in the eventual
// merged Report (e.g. "client_page_view:/[locale]/dashboard" vs a raw
// "workflow_instance_created").
func handleClientReport(reporter *Reporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var cr ClientReport
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "failed to read client report body", http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(body, &cr); err != nil {
			http.Error(w, "malformed client report body", http.StatusBadRequest)
			return
		}
		for route, n := range cr.PageViewCounts {
			reporter.RecordFeatureEvent("client_page_view:"+route, n)
		}
		for event, n := range cr.FeatureEventCounts {
			reporter.RecordFeatureEvent("client_feature:"+event, n)
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
