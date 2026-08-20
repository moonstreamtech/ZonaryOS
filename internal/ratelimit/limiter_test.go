// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/ratelimit"
)

func testLimits() ratelimit.Limits {
	return ratelimit.Limits{Standard: 100, Bulk: 10, AI: 5, Export: 2, AnalyticsIngest: 500}
}

// TestLimiter_101stRequestInAMinuteReturns429WithRetryAfter is this
// batch's own required test: the 101st standard-category request within
// the same minute (the burst equals the full per-minute allowance, see
// Limiter's own doc comment) must be rejected with 429 and a
// Retry-After header - the first 100 must all succeed.
func TestLimiter_101stRequestInAMinuteReturns429WithRetryAfter(t *testing.T) {
	limiter := ratelimit.NewLimiter(testLimits())
	handler := ratelimit.Middleware(limiter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/firms/3aa7de0c-a5a2-8181-ac86-ef800f628f5d/products", nil)

	for i := 0; i < 100; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected the 101st request to be rejected with 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected a Retry-After header on the 429 response")
	}
}

// TestLimiter_DifferentFirmsHaveIndependentBudgets confirms the
// (firm, category) key choice: exhausting one firm's own standard
// budget must not affect a different firm's own requests against the
// same category.
func TestLimiter_DifferentFirmsHaveIndependentBudgets(t *testing.T) {
	limiter := ratelimit.NewLimiter(ratelimit.Limits{Standard: 1, Bulk: 10, AI: 5, Export: 2, AnalyticsIngest: 500})
	handler := ratelimit.Middleware(limiter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	firmA := httptest.NewRequest(http.MethodGet, "/api/firms/3aa7de0c-a5a2-8181-ac86-ef800f628f5d/products", nil)
	firmB := httptest.NewRequest(http.MethodGet, "/api/firms/4bb7de0c-a5a2-8181-ac86-ef800f628f5d/products", nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, firmA)
	if rec.Code != http.StatusOK {
		t.Fatalf("firm A's first request: expected 200, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, firmA)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("firm A's second request: expected 429 (budget of 1), got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, firmB)
	if rec.Code != http.StatusOK {
		t.Fatalf("firm B's own first request should still succeed even though firm A is exhausted, got %d", rec.Code)
	}
}

// TestLimiter_ConcurrentGoroutineWritesAreSafe is this batch's own
// required test: concurrent Allow calls across many goroutines must not
// race (go test -race is what actually proves this) and must never let
// more than the configured burst through for one key.
func TestLimiter_ConcurrentGoroutineWritesAreSafe(t *testing.T) {
	limiter := ratelimit.NewLimiter(ratelimit.Limits{Standard: 50, Bulk: 10, AI: 5, Export: 2, AnalyticsIngest: 500})

	const goroutines = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowedCount := 0

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _ := limiter.Allow("firm-x", ratelimit.CategoryStandard)
			if allowed {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowedCount != 50 {
		t.Errorf("expected exactly 50 of 100 concurrent requests to be allowed (the configured burst), got %d", allowedCount)
	}
}

// TestClassify_MapsKnownRouteShapesToTheirOwnCategory confirms
// Classify's own routing table against every real endpoint shape this
// batch's brief names.
func TestClassify_MapsKnownRouteShapesToTheirOwnCategory(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   ratelimit.Category
	}{
		{http.MethodPost, "/api/firms/x/invoices/bulk-status", ratelimit.CategoryBulk},
		{http.MethodPost, "/api/firms/x/workflow-instances/bulk-transition", ratelimit.CategoryBulk},
		{http.MethodPost, "/api/firms/x/ai/suggest-workflow", ratelimit.CategoryAI},
		{http.MethodPost, "/api/firms/x/ai/suggest-report", ratelimit.CategoryAI},
		{http.MethodGet, "/api/firms/x/export", ratelimit.CategoryExport},
		{http.MethodPost, "/api/firms/x/analytics/events", ratelimit.CategoryAnalyticsIngest},
		{http.MethodGet, "/api/firms/x/products", ratelimit.CategoryStandard},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		if got := ratelimit.Classify(req); got != tt.want {
			t.Errorf("Classify(%s %s) = %q, want %q", tt.method, tt.path, got, tt.want)
		}
	}
}
