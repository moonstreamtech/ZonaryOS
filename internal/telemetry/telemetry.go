// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package telemetry is aggregate, non-content usage telemetry: how many
// requests hit which endpoint, how many times a given client-visible
// feature-success event fired, which source IPs made requests - never
// what was in a request, a page, or a business record. See Reporter's
// doc comment for the absolute rule this package is built around, and
// the "default-disabled guarantee" section for the same structural
// rigor internal/license.Verifier's own doc comment establishes for its
// package (Open Points item 32's prior batch - this package is held to
// the identical bar, non-negotiably, because this batch additionally
// collects source IP addresses, real privacy-sensitive data - see the
// KVKK cross-reference this batch adds alongside Open Points item 33).
//
// # What this package does NOT do
//
//   - It never inspects a request body, a response body, a page's
//     rendered content, or any business-data field. Every counter this
//     package increments is either an endpoint path + HTTP method, a
//     named feature-event string chosen by the caller (e.g.
//     "workflow_instance_created"), or a source IP - identifiers and
//     counts, never content.
//   - It never blocks or fails a request. A reporting-flush failure
//     (central address unreachable, timeout, non-200) is logged and
//     dropped, never propagated into any request-handling path - see
//     Reporter.flushOnce's own comment for where that boundary is drawn.
//   - It never runs anything - no counters, no background goroutine, no
//     HTTP calls - unless ZONARYOS_TELEMETRY_ENABLED is exactly "true".
//     See NewReporter and Reporter.Record's own comments for the
//     structural proof, matching license.Verifier.Allow's "first
//     statement" pattern exactly.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/discovery"
)

// DefaultFlushInterval is how often a Reporter with reporting enabled
// batches and sends its accumulated counts to the resolved central
// address.
//
// PROVISIONAL DEFAULT, NOT A DECIDED value - same honesty as
// internal/discovery.DefaultTTL and internal/license's
// RecheckInterval/DefaultGracePeriod: no Vision/Open Points text
// specifies a reporting cadence. 5 minutes is a defensible starting
// guess (frequent enough that aggregate usage data isn't stale by more
// than a few minutes; infrequent enough that this is obviously
// negligible network/CPU cost - one small batched POST every 5 minutes,
// not per-request). Revisit once there's a real central server with an
// actual ingestion-cost/freshness tradeoff to tune against.
const DefaultFlushInterval = 5 * time.Minute

// DefaultTimeout bounds a single report-flush HTTP call.
const DefaultTimeout = 5 * time.Second

// Report is the batched payload a Reporter sends to
// POST {central}/telemetry/report. Every field is a count keyed by an
// identifier, never a content sample - see this package's doc comment.
type Report struct {
	// InstallationStartedAt marks when this Reporter (i.e. this server
	// process) began accumulating the batch being sent - lets a real
	// future central server sanity-check reporting cadence, without
	// this installation needing any persistent identity beyond "some
	// process reported this batch."
	PeriodStart time.Time `json:"periodStart"`
	PeriodEnd   time.Time `json:"periodEnd"`

	// RequestCounts is endpoint (method + path pattern, e.g.
	// "GET /api/firms/{firmID}") -> request count for that period -
	// never the request body, query values, or path parameter values
	// themselves.
	RequestCounts map[string]int `json:"requestCounts,omitempty"`
	// SourceIPCounts is source IP address -> request count for that
	// period. This is the one field with the most privacy weight (see
	// package doc comment and docs/OPEN_POINTS.md's KVKK
	// cross-reference) - it is still only a count per already-visible
	// network-layer address, never tied to any request content.
	SourceIPCounts map[string]int `json:"sourceIpCounts,omitempty"`
	// FeatureEventCounts is a named feature-success event (e.g.
	// "workflow_instance_created", "sale_completed",
	// "client_page_view:/dashboard") -> count for that period -
	// reported by both the Go server (via Reporter.Record) and the web
	// frontend's own batching client (POSTed to this same Reporter
	// through its client-report ingestion handler, see handlers.go).
	FeatureEventCounts map[string]int `json:"featureEventCounts,omitempty"`
}

// isEmpty reports whether r has nothing worth sending - Reporter's flush
// loop skips a POST entirely for an empty period rather than sending a
// no-op batch every interval forever on an idle installation.
func (r Report) isEmpty() bool {
	return len(r.RequestCounts) == 0 && len(r.SourceIPCounts) == 0 && len(r.FeatureEventCounts) == 0
}

// Reporter is this package's one stateful object: an in-memory batching
// counter set, flushed periodically to whatever internal/discovery
// currently resolves as the central address.
//
// # The default-disabled guarantee
//
// A Reporter built with enabled=false (i.e. ZONARYOS_TELEMETRY_ENABLED is
// unset or not exactly "true" - see internal/platform/config) is `nil`.
// NewReporter returns nil, not a disabled-but-allocated struct, when
// enabled is false - so every method on *Reporter (Record,
// RecordFeatureEvent, and the http.Handler Middleware returns) is called
// on a nil receiver in that case and is written, exactly like
// license.Verifier.Allow, to check for nil as the literal first
// statement and do nothing else. There is no counter map allocated, no
// mutex contended, no background goroutine started (Reporter.Start
// itself is never called from cmd/server/main.go unless
// cfg.TelemetryEnabled is true - see that file's comment), and
// Middleware, called unconditionally by cmd/server/main.go the same way
// identity.Middleware is, wraps the next handler with a passthrough that
// adds no work at all when r is nil - see Middleware's own comment.
// This is intentionally not "usually a no-op when disabled" - it is
// structurally guaranteed by the nil receiver having nothing to read,
// the same shape license.Verifier's doc comment describes for its own
// default-disabled guarantee.
type Reporter struct {
	client   *discovery.Client
	interval time.Duration
	timeout  time.Duration
	httpDo   func(req *http.Request) (*http.Response, error)
	now      func() time.Time

	mu          sync.Mutex
	periodStart time.Time
	requests    map[string]int
	sourceIPs   map[string]int
	features    map[string]int
}

// Option customizes a Reporter built by NewReporter - test-only hooks,
// production callers (cmd/server) don't need any.
type Option func(*Reporter)

// WithFlushInterval overrides DefaultFlushInterval.
func WithFlushInterval(d time.Duration) Option {
	return func(r *Reporter) { r.interval = d }
}

// WithHTTPDo overrides the function used to perform the report POST -
// test-only hook.
func WithHTTPDo(do func(req *http.Request) (*http.Response, error)) Option {
	return func(r *Reporter) { r.httpDo = do }
}

// WithClock overrides the time source used for period bookkeeping -
// test-only hook.
func WithClock(now func() time.Time) Option {
	return func(r *Reporter) { r.now = now }
}

// NewReporter builds a Reporter, or returns nil when enabled is false -
// see Reporter's doc comment for why that's the entire default-disabled
// guarantee, not merely a convention. client is the shared
// internal/discovery.Client instance also used by internal/license
// (when it needs one) - see internal/discovery's package doc comment for
// why there is exactly one discovery/cache implementation, not one per
// consumer. When enabled is false, client is never even read.
func NewReporter(enabled bool, client *discovery.Client, opts ...Option) *Reporter {
	if !enabled {
		return nil
	}
	r := &Reporter{
		client:      client,
		interval:    DefaultFlushInterval,
		timeout:     DefaultTimeout,
		httpDo:      http.DefaultClient.Do,
		now:         time.Now,
		periodStart: time.Now(),
		requests:    make(map[string]int),
		sourceIPs:   make(map[string]int),
		features:    make(map[string]int),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// RecordRequest increments the count for one server-side HTTP request -
// endpoint identifier (method + registered pattern, e.g.
// "GET /api/firms/{firmID}" - never the resolved path parameter values)
// and source IP. A no-op on a nil Reporter - see Reporter's doc comment.
func (r *Reporter) RecordRequest(endpoint, sourceIP string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests[endpoint]++
	if sourceIP != "" {
		r.sourceIPs[sourceIP]++
	}
}

// RecordFeatureEvent increments the count for one named feature-usage
// event (e.g. "workflow_instance_created") by n (n is normally 1 for a
// single server-side event, or whatever batched count a client report
// already aggregated - see handlers.go's client-report ingestion). A
// no-op on a nil Reporter.
func (r *Reporter) RecordFeatureEvent(event string, n int) {
	if r == nil || event == "" || n <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.features[event] += n
}

// snapshot copies out the current period's counts and resets the
// in-memory counters for the next period - called only from flushOnce,
// under r.mu, so a report in flight never races with new counts landing
// mid-batch (Record*/snapshot both take r.mu; the HTTP send itself
// happens outside the lock, see flushOnce).
func (r *Reporter) snapshot() Report {
	r.mu.Lock()
	defer r.mu.Unlock()

	rep := Report{
		PeriodStart:        r.periodStart,
		PeriodEnd:          r.now(),
		RequestCounts:      r.requests,
		SourceIPCounts:     r.sourceIPs,
		FeatureEventCounts: r.features,
	}
	r.periodStart = r.now()
	r.requests = make(map[string]int)
	r.sourceIPs = make(map[string]int)
	r.features = make(map[string]int)
	return rep
}

// Start runs the periodic flush loop until ctx is cancelled. Called from
// cmd/server/main.go's background goroutine only when
// cfg.TelemetryEnabled is true, mirroring exactly how
// internal/license.RecheckInterval's ticker goroutine is only started
// when ZONARYOS_LICENSE_ENFORCEMENT=true - a deployment that never opts
// in never has this goroutine running at all, not merely idling. Safe to
// call on a nil Reporter (returns immediately) as an extra guard, though
// cmd/server/main.go's own gating already prevents that call from
// happening.
func (r *Reporter) Start(ctx context.Context) {
	if r == nil {
		return
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.flushOnce(ctx)
		}
	}
}

// flushOnce sends one batch to the currently resolved central address.
//
// # Resilience boundary
//
// Every failure path here (discovery resolution producing an unusable
// address is impossible by internal/discovery's own design - see that
// package's Resolve - but the subsequent HTTP POST can still fail for
// any ordinary network reason) is caught and logged right here, at the
// literal bottom of the call stack this goroutine runs - nothing calls
// flushOnce except Start's own ticker loop, so there is no request-
// handling path anywhere above this function for an error to propagate
// into. This mirrors this package's doc comment's "never blocks or
// fails a request" guarantee being structurally obvious from the code,
// the same way license.Verifier.Allow's default-disabled guarantee is
// obvious from its first line, not merely documented.
func (r *Reporter) flushOnce(ctx context.Context) {
	rep := r.snapshot()
	if rep.isEmpty() {
		return
	}

	body, err := json.Marshal(rep)
	if err != nil {
		slog.Warn("telemetry: marshal report dropped", "err", err)
		return
	}

	central := r.client.Resolve(ctx)
	reqCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, central+"/telemetry/report", bytes.NewReader(body))
	if err != nil {
		slog.Warn("telemetry: build report request dropped", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpDo(req)
	if err != nil {
		slog.Warn("telemetry: flush failed, will retry next cycle with fresh counts", "central", central, "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		slog.Warn("telemetry: flush rejected, dropped", "central", central, "status", resp.StatusCode)
	}
}

// Middleware wraps the whole mux with per-request telemetry counting -
// called once, around the entire *http.ServeMux, by cmd/server/main.go
// (net/http.Server.Handler = telemetry.Middleware(reporter, mux)(mux)),
// unlike identity.Middleware which each module wraps its own routes
// with - this one needs mux itself (not just the next handler in the
// chain) so it can ask mux.Handler(req) for the *registered pattern*
// (e.g. "GET /api/firms/{firmID}") before dispatching, the same
// identifier-not-value distinction RecordRequest's own doc comment
// describes. On a nil Reporter this is a pure passthrough - see the
// comment inline below - so a deployment that never sets
// ZONARYOS_TELEMETRY_ENABLED=true pays no per-request cost from this
// package's existence beyond one nil check per request.
func Middleware(r *Reporter, mux *http.ServeMux) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if r == nil {
			// Structural default-disabled guarantee: when telemetry is
			// disabled, Middleware doesn't even wrap next in a
			// telemetry-aware closure - it hands back next itself,
			// completely unchanged, so there is no wrapper allocated,
			// no additional function call per request, nothing for a
			// profiler or a code reader to find beyond this branch.
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			_, pattern := mux.Handler(req)
			if pattern == "" {
				pattern = req.URL.Path
			}
			endpoint := fmt.Sprintf("%s %s", req.Method, pattern)
			r.RecordRequest(endpoint, sourceIP(req))
			next.ServeHTTP(w, req)
		})
	}
}

// sourceIP extracts the request's source IP, preferring RemoteAddr (the
// actual TCP peer nginx connects from, per docs/DEPLOYMENT.md's reverse-
// proxy topology - this codebase's nginx config does not currently
// forward X-Forwarded-For into the backend, so RemoteAddr already
// reflects the real connecting peer for this deployment's actual
// topology; a client-supplied X-Forwarded-For header is deliberately not
// trusted here since it would let any caller spoof the counted address).
func sourceIP(r *http.Request) string {
	host := r.RemoteAddr
	if idx := lastColon(host); idx >= 0 {
		return host[:idx]
	}
	return host
}

func lastColon(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}
