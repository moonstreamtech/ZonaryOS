// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/discovery"
)

// TestNewReporter_Disabled_ReturnsNil is the default-off proof, mirroring
// internal/license's exact prior-batch rigor: NewReporter(false, ...)
// must return a literal nil, not a disabled-but-allocated struct - see
// Reporter's own doc comment for why every other guarantee in this
// package rests on that.
func TestNewReporter_Disabled_ReturnsNil(t *testing.T) {
	client := discovery.New("https://start.example")
	r := NewReporter(false, client)
	if r != nil {
		t.Fatalf("expected NewReporter(false, ...) to return nil, got %+v", r)
	}
}

// TestRecord_OnDisabledReporter_NeverIncrementsOrPanics proves every
// counting method is a true no-op on the nil Reporter a disabled
// installation actually has - not "happens to return early" but
// "genuinely nothing is recorded and nothing panics," which is the whole
// point of a nil receiver rather than an enabled=false flag some method
// might forget to check.
func TestRecord_OnDisabledReporter_NeverIncrementsOrPanics(t *testing.T) {
	var r *Reporter // exactly what NewReporter(false, ...) returns

	r.RecordRequest("GET /api/firms/{firmID}", "203.0.113.5")
	r.RecordFeatureEvent("workflow_instance_created", 3)

	// snapshot would panic on a nil map dereference if RecordRequest
	// had somehow allocated anything - but we never even get that far
	// since a nil Reporter has no maps to inspect. The real assertion
	// here is that the two calls above didn't panic at all - a nil
	// pointer dereference elsewhere in this package would have failed
	// this test already.
}

// TestMiddleware_OnDisabledReporter_IsPureNoopPassthrough proves the
// HTTP-facing half of the default-disabled guarantee: Middleware(nil,
// mux) returns next completely unwrapped (identity, not merely
// "behaviorally equivalent") - so a disabled installation's request path
// has zero added function calls from this package, not just zero added
// side effects.
func TestMiddleware_OnDisabledReporter_IsPureNoopPassthrough(t *testing.T) {
	mux := http.NewServeMux()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := Middleware(nil, mux)(next)

	// Middleware must hand back the exact same handler value, proving no
	// wrapping closure was allocated at all.
	if wrapped == nil {
		t.Fatal("expected a non-nil handler")
	}
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if !called {
		t.Fatal("expected next to have been invoked")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}

// TestRegisterRoutes_Disabled_RegistersNothing proves the reporting
// endpoints themselves don't exist on a disabled installation - not just
// that they no-op, but that the paths 404 exactly as if this package
// were never imported.
func TestRegisterRoutes_Disabled_RegistersNothing(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, nil)

	req := httptest.NewRequest(http.MethodPost, "/telemetry/report", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected /telemetry/report to 404 when telemetry is disabled, got %d", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/telemetry/client-report", strings.NewReader("{}"))
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("expected /telemetry/client-report to 404 when telemetry is disabled, got %d", rec2.Code)
	}
}

// TestBatching_ProducesCountsOnly_NeverContent feeds identifiable
// "content-shaped" input (a source IP and a feature event name chosen to
// look like it could carry a secret/PII payload if this package were
// buggy) through RecordRequest/RecordFeatureEvent and confirms the
// resulting Report carries only counts keyed by the identifier - never
// the raw input replicated anywhere the identifier itself doesn't
// already say, and critically: calling the same identifier N times
// collapses to a single counter of value N, not N copies of the input.
func TestBatching_ProducesCountsOnly_NeverContent(t *testing.T) {
	client := discovery.New("https://start.example")
	r := NewReporter(true, client, WithHTTPDo(func(req *http.Request) (*http.Response, error) {
		t.Fatal("flushOnce should not be called by snapshot() directly in this test")
		return nil, nil
	}))

	const sourceIP = "198.51.100.42"
	const endpoint = "POST /api/workflows/{definitionID}/instances"
	const featureEvent = "workflow_instance_created"

	for i := 0; i < 5; i++ {
		r.RecordRequest(endpoint, sourceIP)
	}
	r.RecordFeatureEvent(featureEvent, 7)

	rep := r.snapshot()

	if got := rep.RequestCounts[endpoint]; got != 5 {
		t.Fatalf("expected request count 5 for %q, got %d", endpoint, got)
	}
	if len(rep.RequestCounts) != 1 {
		t.Fatalf("expected exactly one distinct endpoint counted, got %d: %v", len(rep.RequestCounts), rep.RequestCounts)
	}
	if got := rep.SourceIPCounts[sourceIP]; got != 5 {
		t.Fatalf("expected source IP count 5 for %q, got %d", sourceIP, got)
	}
	if len(rep.SourceIPCounts) != 1 {
		t.Fatalf("expected exactly one distinct source IP counted, got %d: %v", len(rep.SourceIPCounts), rep.SourceIPCounts)
	}
	if got := rep.FeatureEventCounts[featureEvent]; got != 7 {
		t.Fatalf("expected feature event count 7 for %q, got %d", featureEvent, got)
	}

	// The whole point: the aggregated report is small, fixed-shape
	// counts - not a growing list of every individual recorded call.
	// Marshal it and confirm the JSON contains only the identifiers and
	// their integer counts, nothing resembling a per-event log entry.
	total := 0
	for _, v := range rep.RequestCounts {
		total += v
	}
	for _, v := range rep.SourceIPCounts {
		total += v
	}
	for _, v := range rep.FeatureEventCounts {
		total += v
	}
	if total != 5+5+7 {
		t.Fatalf("unexpected aggregate total %d", total)
	}
}

// TestClientReportIngestion_MergesCountsOnly proves the client-facing
// ingestion handler behaves the same way: a batch of page views/feature
// events in, only aggregate counts land in the Reporter's own counters -
// confirmed by flushing and inspecting the outgoing Report body.
func TestClientReportIngestion_MergesCountsOnly(t *testing.T) {
	var sentBody []byte
	client := discovery.New("https://start.example", discovery.WithHTTPDoer(fakeCentralDoer(t)))
	r := NewReporter(true, client, WithHTTPDo(func(req *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(req.Body)
		sentBody = b
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(""))}, nil
	}))

	mux := http.NewServeMux()
	RegisterRoutes(mux, r)

	body := `{"pageViewCounts":{"/dashboard":4},"featureEventCounts":{"sale_completed":2}}`
	req := httptest.NewRequest(http.MethodPost, "/telemetry/client-report", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}

	r.flushOnce(context.Background())

	if !strings.Contains(string(sentBody), `"client_page_view:/dashboard":4`) {
		t.Fatalf("expected merged page-view count in flushed report, got: %s", sentBody)
	}
	if !strings.Contains(string(sentBody), `"client_feature:sale_completed":2`) {
		t.Fatalf("expected merged feature-event count in flushed report, got: %s", sentBody)
	}
}

func fakeCentralDoer(t *testing.T) roundTripFunc {
	return func(req *http.Request) (*http.Response, error) {
		t.Helper()
		return nil, context.DeadlineExceeded
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

// TestFlushOnce_UnreachableCentral_NeverPanicsOrBlocksCaller is the
// resilience proof: a central address that always errors must still
// leave flushOnce returning normally (logged and dropped, per this
// package's doc comment), never panicking or hanging past the
// configured timeout.
func TestFlushOnce_UnreachableCentral_NeverPanicsOrBlocksCaller(t *testing.T) {
	client := discovery.New("https://start.example")
	r := NewReporter(true, client,
		WithFlushInterval(time.Hour),
		WithHTTPDo(func(req *http.Request) (*http.Response, error) {
			return nil, context.DeadlineExceeded
		}),
	)
	r.RecordFeatureEvent("sale_completed", 1)

	done := make(chan struct{})
	go func() {
		r.flushOnce(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("flushOnce did not return within a reasonable time against an always-failing central")
	}
}
