// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package discovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func httpBody(s string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(s))
}

// stubDoer lets a test script a sequence of responses/errors without a
// real listener.
type stubDoer struct {
	mu    sync.Mutex
	fn    func(req *http.Request) (*http.Response, error)
	calls int
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.fn(req)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       httpBody(body),
		Header:     http.Header{},
	}
}

func TestResolve_FirstRun_NoCacheYet_FallsBackToStartURL(t *testing.T) {
	doer := &stubDoer{fn: func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("central unreachable")
	}}
	c := New("https://start.example", WithHTTPDoer(doer))

	got := c.Resolve(context.Background())
	if got != "https://start.example" {
		t.Fatalf("expected fallback to StartURL, got %q", got)
	}
}

func TestResolve_FallsBackToLastKnownGood_WhenDiscoveryUnreachable(t *testing.T) {
	first := true
	doer := &stubDoer{fn: func(req *http.Request) (*http.Response, error) {
		if first {
			first = false
			return jsonResponse(`{"centralUrl":"https://central.example"}`), nil
		}
		return nil, errors.New("central unreachable")
	}}

	fakeNow := time.Now()
	c := New("https://start.example", WithHTTPDoer(doer), WithTTL(time.Millisecond), WithClock(func() time.Time { return fakeNow }))

	// First resolve succeeds and caches https://central.example.
	got := c.Resolve(context.Background())
	if got != "https://central.example" {
		t.Fatalf("expected central.example, got %q", got)
	}

	// Advance the fake clock past TTL, then make the next fetch fail -
	// Resolve must fall back to the LAST-KNOWN-GOOD cached address
	// (central.example), never to StartURL and never to a hardcoded
	// value, even though the cache is technically stale.
	fakeNow = fakeNow.Add(time.Hour)
	got = c.Resolve(context.Background())
	if got != "https://central.example" {
		t.Fatalf("expected fallback to last-known-good https://central.example, got %q", got)
	}
	if doer.calls != 2 {
		t.Fatalf("expected exactly 2 fetch attempts, got %d", doer.calls)
	}
}

func TestResolve_PicksUpChangedAddress_AfterTTLExpires(t *testing.T) {
	step := 0
	doer := &stubDoer{fn: func(req *http.Request) (*http.Response, error) {
		step++
		if step == 1 {
			return jsonResponse(`{"centralUrl":"https://central-v1.example"}`), nil
		}
		return jsonResponse(`{"centralUrl":"https://central-v2.example"}`), nil
	}}

	fakeNow := time.Now()
	c := New("https://start.example", WithHTTPDoer(doer), WithTTL(time.Minute), WithClock(func() time.Time { return fakeNow }))

	got := c.Resolve(context.Background())
	if got != "https://central-v1.example" {
		t.Fatalf("expected central-v1.example, got %q", got)
	}

	// Within TTL: cached value returned, no second fetch.
	got = c.Resolve(context.Background())
	if got != "https://central-v1.example" {
		t.Fatalf("expected cached central-v1.example within TTL, got %q", got)
	}
	if doer.calls != 1 {
		t.Fatalf("expected exactly 1 fetch while cache is fresh, got %d", doer.calls)
	}

	// Simulate real time passing beyond TTL.
	fakeNow = fakeNow.Add(2 * time.Minute)
	got = c.Resolve(context.Background())
	if got != "https://central-v2.example" {
		t.Fatalf("expected resolved address to change to central-v2.example after TTL expiry, got %q", got)
	}
	if doer.calls != 2 {
		t.Fatalf("expected exactly 2 fetches total, got %d", doer.calls)
	}
}

func TestResolve_WithinTTL_DoesNotRefetch(t *testing.T) {
	doer := &stubDoer{fn: func(req *http.Request) (*http.Response, error) {
		return jsonResponse(`{"centralUrl":"https://central.example"}`), nil
	}}
	c := New("https://start.example", WithHTTPDoer(doer), WithTTL(time.Hour))

	c.Resolve(context.Background())
	c.Resolve(context.Background())
	c.Resolve(context.Background())

	if doer.calls != 1 {
		t.Fatalf("expected exactly 1 fetch across 3 Resolve calls within TTL, got %d", doer.calls)
	}
}
