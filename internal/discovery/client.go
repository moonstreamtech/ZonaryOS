// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// DefaultTTL is how long a resolved central address is trusted before
// Client.Resolve re-fetches the well-known document.
//
// PROVISIONAL DEFAULT, NOT A DECIDED value - matching
// internal/license.DefaultGracePeriod/RecheckInterval's own honesty about
// this: nothing in Vision or Open Points item 34 specifies how often an
// installation should re-check for a migrated central address. 1 hour is
// a defensible starting guess (frequent enough that a real future
// migration propagates to long-running installations within a business
// day even if nobody restarts them; infrequent enough that this is
// obviously negligible background load - one small HTTP GET an hour, to
// an address the installation already has cached and is not otherwise
// depending on since nothing real reads the resolved value for anything
// operationally critical yet). Revisit once item 34's real central-server
// infrastructure exists and there's an actual SLA to design against.
const DefaultTTL = time.Hour

// DefaultTimeout bounds a single well-known fetch attempt, so an
// unreachable or slow central address can never block whatever goroutine
// called Resolve for longer than this - resilience matters more here than
// for most HTTP calls in this codebase, since Resolve is meant to be safe
// to call from background flush loops that must never wedge.
const DefaultTimeout = 5 * time.Second

// Client resolves "where is the central server right now," backed by a
// cached last-known-good address.
//
// # Cache / TTL / fallback design
//
// Resolve first checks whether the cached address is still within TTL; if
// so, it's returned immediately with no network call. Once the cache is
// stale, Resolve fetches GET {current address}/.well-known/zonaryos-central
// (i.e. it walks forward from wherever it last successfully resolved to,
// not back to the originally configured StartURL - this is what makes a
// real future migration a *chain*: installation A was told about B, B was
// told about C, and A's client, still holding B as its last-known-good,
// asks B "where now?" and learns C, without ever needing to know about C
// directly).
//
// If that fetch fails (network error, non-200, malformed body, or an
// empty centralUrl in the body), Resolve falls back to the LAST-KNOWN-GOOD
// cached address - never to a hardcoded default, and never to StartURL
// once a real resolution has ever succeeded. This is the property that
// makes migration seamless for a rarely-restarted installation: as long
// as the last address it successfully resolved is still reachable at
// all, that address's own well-known document is what eventually points
// it further forward, even if the *original* StartURL it was configured
// with years ago has long since gone dark.
//
// # First-run behavior (no successful resolution yet)
//
// If Resolve has never successfully fetched a well-known document (fresh
// process, empty cache) and the first fetch also fails, there is no
// "last-known-good" to fall back to by definition - Resolve falls back to
// StartURL itself. Reasoning: StartURL is the only address this
// installation was ever told to trust, so treating it as the
// zeroth-generation "last known good" is the only option that doesn't
// either (a) return an empty/invalid address to the caller, which every
// caller (internal/telemetry's flush loop) would then have to
// special-case anyway, or (b) hardcode some other default address into
// this package, which is exactly the hardcoded-fallback failure mode this
// whole design exists to avoid. It is explicitly a starting point, not a
// claim that StartURL is confirmed reachable - a caller that then also
// fails to use the returned address (e.g. the telemetry report POST
// itself 404s/times out) simply drops that report and tries again next
// cycle, same as any other transient failure (see internal/telemetry's
// own resilience guarantee).
type Client struct {
	startURL string
	ttl      time.Duration
	timeout  time.Duration
	doer     httpDoer
	now      func() time.Time

	mu           sync.Mutex
	cachedURL    string
	resolvedOnce bool
	fetchedAt    time.Time
}

// httpDoer is the minimal interface Client depends on for its HTTP call -
// satisfied by *http.Client, and small enough that tests can substitute a
// stub that fails on demand without spinning up a real listener.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Option customizes a Client built by New - used by tests to inject a
// fake clock and/or a stub HTTP doer; production callers (cmd/server)
// don't need any.
type Option func(*Client)

// WithTTL overrides DefaultTTL.
func WithTTL(ttl time.Duration) Option {
	return func(c *Client) { c.ttl = ttl }
}

// WithTimeout overrides DefaultTimeout.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) { c.timeout = timeout }
}

// WithHTTPDoer overrides the HTTP client used to fetch the well-known
// document - test-only hook (production always uses http.DefaultClient
// via New).
func WithHTTPDoer(doer httpDoer) Option {
	return func(c *Client) { c.doer = doer }
}

// WithClock overrides the time source Resolve uses for TTL comparisons -
// test-only hook, so a TTL-expiry test can simulate time passing without
// an actual sleep.
func WithClock(now func() time.Time) Option {
	return func(c *Client) { c.now = now }
}

// New builds a Client whose starting/fallback-of-last-resort address is
// startURL (ZONARYOS_DISCOVERY_START_URL - see internal/platform/config;
// for today's single-instance reality this is simply the installation's
// own ZONARYOS_PUBLIC_URL, the same self-referential starting point
// HandleWellKnown itself answers with).
func New(startURL string, opts ...Option) *Client {
	c := &Client{
		startURL: strings.TrimRight(startURL, "/"),
		ttl:      DefaultTTL,
		timeout:  DefaultTimeout,
		doer:     http.DefaultClient,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Resolve returns the current best-known central-server base URL (no
// trailing slash). See Client's doc comment for the full cache/TTL/
// fallback/first-run design; this method never returns an error - by
// design there is always some address to hand back (StartURL at worst),
// and a caller that then also fails to reach that address should treat
// that as an ordinary transient network failure, not a discovery failure.
func (c *Client) Resolve(ctx context.Context) string {
	c.mu.Lock()
	fresh := c.resolvedOnce && c.now().Sub(c.fetchedAt) < c.ttl
	current := c.cachedURL
	if !c.resolvedOnce {
		current = c.startURL
	}
	c.mu.Unlock()

	if fresh {
		return current
	}

	fetched, err := c.fetch(ctx, current)
	if err != nil {
		// Fetch failed - fall back to last-known-good (or StartURL, on a
		// genuine first-ever resolution). Deliberately does NOT bump
		// fetchedAt: a failed attempt must not extend the TTL window and
		// suppress the next retry, or a permanently-unreachable central
		// address would look "fresh" forever after one failure.
		return current
	}

	c.mu.Lock()
	c.cachedURL = fetched
	c.resolvedOnce = true
	c.fetchedAt = c.now()
	c.mu.Unlock()
	return fetched
}

func (c *Client) fetch(ctx context.Context, fromURL string) (string, error) {
	if fromURL == "" {
		return "", fmt.Errorf("discovery: no address to fetch the well-known document from")
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fromURL+"/.well-known/zonaryos-central", nil)
	if err != nil {
		return "", fmt.Errorf("discovery: build request: %w", err)
	}

	resp, err := c.doer.Do(req)
	if err != nil {
		return "", fmt.Errorf("discovery: fetch %s: %w", fromURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discovery: %s returned status %d", fromURL, resp.StatusCode)
	}

	var doc WellKnownDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", fmt.Errorf("discovery: decode well-known document from %s: %w", fromURL, err)
	}

	central := strings.TrimRight(strings.TrimSpace(doc.CentralURL), "/")
	if central == "" {
		return "", fmt.Errorf("discovery: %s returned an empty centralUrl", fromURL)
	}
	return central, nil
}
