// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package middleware_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/platform/middleware"
)

// TestPanicRecovery_ReturnsCleanFiveHundredWithoutCrashing is the
// batch's own required test: a panicking handler must not crash the
// process - PanicRecovery must catch it and return a clean 500.
func TestPanicRecovery_ReturnsCleanFiveHundredWithoutCrashing(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	handler := middleware.PanicRecovery(panicking)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)

	// If PanicRecovery didn't catch this, the panic would propagate up
	// through this very call and fail the test (Go's testing package
	// converts an unrecovered panic in the test goroutine into a test
	// failure, not a process crash - but a REAL net/http server with no
	// recovery middleware crashes the entire process for every other
	// in-flight request too, which is exactly what this middleware
	// exists to prevent; this test still proves the recovery itself
	// works, which is the unit under test here).
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Errorf("expected a clean error body, got %q", rec.Body.String())
	}
}

// TestPanicRecovery_DoesNotAffectANormalRequest confirms the middleware
// is a pure pass-through when nothing panics.
func TestPanicRecovery_DoesNotAffectANormalRequest(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("fine"))
	})
	handler := middleware.PanicRecovery(ok)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}
	if rec.Body.String() != "fine" {
		t.Errorf("expected body %q, got %q", "fine", rec.Body.String())
	}
}

// TestRequestID_SetsHeaderAndContextValue confirms a request ID is
// generated, set on the response header, and retrievable from the
// request's own context inside the wrapped handler.
func TestRequestID_SetsHeaderAndContextValue(t *testing.T) {
	var sawInContext string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawInContext = middleware.RequestIDFromContext(r.Context())
	})
	handler := middleware.RequestID(next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	handler.ServeHTTP(rec, req)

	headerID := rec.Header().Get(middleware.RequestIDHeader)
	if headerID == "" {
		t.Fatal("expected a non-empty request ID response header")
	}
	if sawInContext != headerID {
		t.Errorf("expected the handler's own context request ID %q to match the response header %q", sawInContext, headerID)
	}
}

// TestRequestID_ReusesIncomingHeaderValue confirms a caller-supplied
// request ID (e.g. from an upstream reverse proxy) is honored rather
// than overwritten.
func TestRequestID_ReusesIncomingHeaderValue(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	handler := middleware.RequestID(next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	req.Header.Set(middleware.RequestIDHeader, "caller-supplied-id")
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get(middleware.RequestIDHeader); got != "caller-supplied-id" {
		t.Errorf("expected the incoming request ID to be reused, got %q", got)
	}
}

// TestRequestSizeLimit_RejectsOversizedJSONBody confirms a JSON body
// exceeding maxJSONBytes fails to fully read, while multipart/form-data
// gets the (larger) upload limit instead.
func TestRequestSizeLimit_RejectsOversizedJSONBody(t *testing.T) {
	var readErr error
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	})
	handler := middleware.RequestSizeLimit(10, 1000)(next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/whatever", bytes.NewReader(bytes.Repeat([]byte("a"), 100)))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if readErr == nil {
		t.Fatal("expected reading a body larger than the JSON limit to fail")
	}
}

// TestRequestSizeLimit_AllowsBodyWithinUploadLimitForMultipart confirms
// a multipart/form-data body within the (larger) upload limit, but over
// the JSON limit, still succeeds - proving the two limits are genuinely
// applied per Content-Type, not just the smaller one always winning.
func TestRequestSizeLimit_AllowsBodyWithinUploadLimitForMultipart(t *testing.T) {
	var readErr error
	var n int
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		readErr = err
		n = len(body)
	})
	handler := middleware.RequestSizeLimit(10, 1000)(next)

	rec := httptest.NewRecorder()
	body := bytes.Repeat([]byte("a"), 100)
	req := httptest.NewRequest(http.MethodPost, "/whatever", bytes.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	handler.ServeHTTP(rec, req)

	if readErr != nil {
		t.Fatalf("expected a 100-byte multipart body under the 1000-byte upload limit to read fully, got %v", readErr)
	}
	if n != 100 {
		t.Errorf("expected to read all 100 bytes, got %d", n)
	}
}

// TestRequestTimeout_CancelsContextAfterDeadline confirms a handler that
// outlives the configured timeout observes its own request context
// being cancelled.
func TestRequestTimeout_CancelsContextAfterDeadline(t *testing.T) {
	var sawDone bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			sawDone = true
		case <-time.After(500 * time.Millisecond):
		}
	})
	handler := middleware.RequestTimeout(10 * time.Millisecond)(next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	handler.ServeHTTP(rec, req)

	if !sawDone {
		t.Error("expected the request context to be cancelled once the timeout elapsed")
	}
}

// TestRequestTimeout_ExemptsSSEPath confirms the permission-events SSE
// route is passed through with its context untouched - see
// middleware.RequestTimeout's own doc comment for why.
func TestRequestTimeout_ExemptsSSEPath(t *testing.T) {
	var hadDeadline bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadDeadline = r.Context().Deadline()
	})
	handler := middleware.RequestTimeout(10 * time.Millisecond)(next)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/firms/abc-123/permission-events", nil)
	handler.ServeHTTP(rec, req)

	if hadDeadline {
		t.Error("expected the SSE route to be exempted from RequestTimeout's own deadline")
	}
}

// TestChain_ComposesAllFourMiddlewareInOrder is a light end-to-end
// smoke test of Chain itself: a panic still gets caught even when
// wrapped through the entire real chain (size limit + timeout in
// between), and a request ID still ends up on the response.
func TestChain_ComposesAllFourMiddlewareInOrder(t *testing.T) {
	panicking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	handler := middleware.Chain(panicking, time.Second, 1<<20, 10<<20)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/whatever", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
	if rec.Header().Get(middleware.RequestIDHeader) == "" {
		t.Error("expected a request ID header even on a panicking request")
	}
}

// TestAPIVersionAlias_RewritesV1PrefixToUnversionedPath is the mobile
// API optimization batch's own "/api/v1/ is an additive alias for /api/"
// requirement: a request to /api/v1/firms/x/products must reach the
// exact same handler an unversioned /api/firms/x/products request does.
func TestAPIVersionAlias_RewritesV1PrefixToUnversionedPath(t *testing.T) {
	var gotPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.APIVersionAlias(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/firms/abc/products", nil)
	handler.ServeHTTP(rec, req)

	if gotPath != "/api/firms/abc/products" {
		t.Errorf("expected rewritten path \"/api/firms/abc/products\", got %q", gotPath)
	}
}

// TestAPIVersionAlias_LeavesUnversionedPathUnchanged proves the alias is
// purely additive - an existing, unversioned caller sees no behavior
// change at all.
func TestAPIVersionAlias_LeavesUnversionedPathUnchanged(t *testing.T) {
	var gotPath string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.APIVersionAlias(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/firms/abc/products", nil)
	handler.ServeHTTP(rec, req)

	if gotPath != "/api/firms/abc/products" {
		t.Errorf("expected unchanged path \"/api/firms/abc/products\", got %q", gotPath)
	}
}
