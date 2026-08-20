// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package ratelimit is Part 2 of the tenant-isolation-hardening/security
// batch: per-firm, per-endpoint-category API rate limiting on top of
// golang.org/x/time/rate's own token-bucket implementation (already an
// indirect dependency of this module via other packages - promoting it
// to direct rather than adding a genuinely new one).
//
// Key choice: (firm_id, category), not firm_id alone. A single
// installation-wide "100 req/min per firm" bucket would let one firm's
// legitimate burst of ordinary CRUD traffic starve out its own budget
// for, say, calling the AI suggestion endpoint a single time - the five
// limits this batch's own brief specifies (standard/bulk/AI/export/
// analytics-ingest) are deliberately different by an order of magnitude
// or more precisely because those endpoint shapes have very different
// legitimate call volumes (one bulk operation might reasonably touch
// hundreds of rows; nobody calls an AI suggestion endpoint 100 times a
// minute on purpose). Keying by category as well as firm keeps each
// budget independent, so hitting one ceiling never silently eats into
// another's.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Category is one of this batch's five endpoint tiers - Classify (see
// classify.go) is the one place an HTTP request's method/path maps to a
// Category.
type Category string

const (
	CategoryStandard        Category = "standard"
	CategoryBulk            Category = "bulk"
	CategoryAI              Category = "ai"
	CategoryExport          Category = "export"
	CategoryAnalyticsIngest Category = "analytics_ingest"
)

// Limits is the requests-per-minute ceiling for each Category -
// internal/platform/config.Config's own RateLimit* fields, passed
// through unchanged (this package has no opinion on defaults; that's
// Config.Load's job).
type Limits struct {
	Standard        int
	Bulk            int
	AI              int
	Export          int
	AnalyticsIngest int
}

func (l Limits) perMinute(c Category) int {
	switch c {
	case CategoryBulk:
		return l.Bulk
	case CategoryAI:
		return l.AI
	case CategoryExport:
		return l.Export
	case CategoryAnalyticsIngest:
		return l.AnalyticsIngest
	default:
		return l.Standard
	}
}

// Limiter is a goroutine-safe registry of one token bucket per (firm,
// category) key, created lazily on first use. It never evicts an idle
// bucket - a known, accepted simplification for this batch's own scope
// (a long-running installation with many distinct firms accumulates one
// small *rate.Limiter per firm per category it has ever called, which is
// cheap - each is a handful of words - and bounded by the number of
// firms that actually exist, not by request volume); a real eviction
// policy is future work if this ever becomes a real memory concern, not
// something this batch needed to solve.
type Limiter struct {
	mu       sync.Mutex
	buckets  map[string]*rate.Limiter
	limits   Limits
	rejected map[string]int
}

// NewLimiter builds a Limiter enforcing limits.
func NewLimiter(limits Limits) *Limiter {
	return &Limiter{
		buckets:  make(map[string]*rate.Limiter),
		limits:   limits,
		rejected: make(map[string]int),
	}
}

// Allow reports whether one request against key/category may proceed
// right now. On rejection, retryAfter is how long the caller should wait
// before its next attempt might succeed (used for the Retry-After
// response header) - computed via rate.Limiter.ReserveN and immediately
// Cancel()ed when the request is rejected, so a rejected request never
// actually consumes a token from the bucket (see the x/time/rate
// package's own documented pattern for "check without necessarily
// consuming").
func (l *Limiter) Allow(key string, category Category) (allowed bool, retryAfter time.Duration) {
	bucketKey := key + ":" + string(category)
	limiter := l.bucketFor(bucketKey, category)

	now := time.Now()
	reservation := limiter.ReserveN(now, 1)
	if !reservation.OK() {
		// Only reachable if perMinute(category) resolves to 0 (burst
		// size 0) - Config.Load never allows a non-positive value
		// through, so this is unreachable in practice; failing open
		// would silently defeat the limiter, so this fails closed
		// instead (a mis-configured limit is a bug worth surfacing as
		// "nothing gets through" rather than "the limit is ignored").
		return false, time.Minute
	}
	delay := reservation.Delay()
	if delay > 0 {
		reservation.Cancel()
		l.recordRejection(bucketKey)
		return false, delay
	}
	return true, 0
}

func (l *Limiter) bucketFor(bucketKey string, category Category) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.buckets[bucketKey]; ok {
		return b
	}
	perMinute := l.limits.perMinute(category)
	// Burst equals the full per-minute allowance: a firm that has been
	// idle can spend its entire minute's budget as one instantaneous
	// burst (the same behavior a fixed "100 req/min" ceiling is
	// naturally expected to allow, e.g. loading a dashboard that fires a
	// dozen requests at once) - rate.Limit is refills-per-SECOND, hence
	// perMinute/60.0.
	b := rate.NewLimiter(rate.Limit(float64(perMinute)/60.0), perMinute)
	l.buckets[bucketKey] = b
	return b
}

// recordRejection bumps this batch's own "aggregate, not per-request"
// rejection counter - see log.go's RunLogFlushLoop for where these
// counts actually get logged.
func (l *Limiter) recordRejection(bucketKey string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rejected[bucketKey]++
}

// snapshotAndResetRejections detaches the current rejection-count map
// and starts a fresh one - the same double-buffering swap
// internal/apm.Buffer already uses for its own flush, so log.go's flush
// never holds Limiter's own mutex while it's actually calling slog.
func (l *Limiter) snapshotAndResetRejections() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.rejected) == 0 {
		return nil
	}
	snapshot := l.rejected
	l.rejected = make(map[string]int)
	return snapshot
}
