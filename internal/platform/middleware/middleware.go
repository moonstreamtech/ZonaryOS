// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package middleware is Part 4 of the NATS JetStream/Edge Agent
// completion batch ("server infrastructure hardening"): four small,
// dependency-free net/http middleware - request ID, panic recovery,
// request size limits, and a request timeout - that wrap the ENTIRE
// mux, unlike internal/identity.Middleware (applied per-route, inside
// each module's own RegisterRoutes). internal/platform/httpapi.NewMux
// builds a bare *http.ServeMux with no middleware chain of its own; this
// package's four wrappers are composed once, in cmd/server/main.go,
// around the whole thing - see Chain's own doc comment for the order and
// why it's the order it is.
package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
)

type contextKey int

const requestIDContextKey contextKey = iota

// RequestIDFromContext returns the request ID RequestID attached to ctx,
// or "" if none is present (e.g. in a test that never went through the
// middleware chain).
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey).(string)
	return id
}

// RequestIDHeader is the response header RequestID sets, and the request
// header it honors if the caller already supplied one (e.g. a reverse
// proxy or load balancer that generates its own correlation ID upstream
// of this server) - trusting a caller-supplied ID here is safe: it only
// ever ends up in this server's own logs/response header, never in any
// authorization decision.
const RequestIDHeader = "X-Request-Id"

// MetricsRecorder, when non-nil, is called by RequestID after every
// request completes with (method, path, statusCode, durationMs) - the
// performance monitoring/alerting/operational excellence batch's own
// Part 1 requirement to "wire the existing RequestID middleware to also
// record metrics after each request completes". A package-level func
// var (not a parameter threaded through RequestID/Chain's own already-
// public signatures) is the same wiring pattern
// internal/inventory.SetOutboundDispatcher/internal/crm.SetOutboundDispatcher
// already establish for a cross-cutting hook set once at startup
// (cmd/server/main.go, to internal/apm.Buffer.Record) - internal/platform/middleware
// cannot import internal/apm directly (internal/apm would need this
// package's own RequestIDFromContext-style plumbing, and more
// importantly this package is intentionally dependency-free, imported by
// nearly every other package in this codebase transitively via
// cmd/server/main.go's own Chain call). Defaults to nil (a no-op) so
// every existing caller/test of RequestID that never wires this is
// completely unaffected.
var MetricsRecorder func(method, path string, statusCode, durationMs int)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// RequestID generates (or reuses an incoming) request ID, sets it on the
// response header, attaches it to the request's context (retrievable via
// RequestIDFromContext), and logs one structured line per request
// (method, path, status, duration, requestId) for correlation - the
// "add to slog context for correlation" half of this batch's own design
// brief. A per-request slog line, rather than teaching every existing
// handler's own log statements to thread a request-scoped logger
// through, is the minimal change that still gives every request a
// traceable, joinable record without touching any of the ~40 existing
// route-registering packages.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(RequestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDContextKey, id)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r.WithContext(ctx))
		durationMs := time.Since(start).Milliseconds()
		slog.Info("request", "requestId", id, "method", r.Method, "path", r.URL.Path, "status", rec.status, "durationMs", durationMs)
		if MetricsRecorder != nil {
			MetricsRecorder(r.Method, r.URL.Path, rec.status, int(durationMs))
		}
	})
}

// SecurityHeaders is Part 3 of the tenant-isolation-hardening/security
// batch: defense-in-depth security headers set at the Go application
// level, for any direct access that bypasses nginx (nginx's own config
// already sets equivalent headers for the normal deployment path - this
// is the same "don't rely on a single layer" reasoning
// internal/platform/middleware.RequestSizeLimit already applies at the
// app level even though a reverse proxy could also cap body size).
// Every response from this binary is an API response (this server has
// no static-asset or HTML-rendering surface of its own - the frontend
// is a separate Next.js deployment), so these headers apply
// unconditionally to every route, with no per-route opt-out:
//
//   - X-Content-Type-Options: nosniff - a browser must never guess/
//     override this response's declared Content-Type.
//   - X-Frame-Options: DENY - this API is never meant to be framed.
//   - Cache-Control: no-store - no intermediate cache or browser should
//     ever persist a response that may carry per-firm business data or
//     an auth-sensitive payload (see internal/apikey.CreateAPIKey's own
//     one-time-plaintext-reveal contract, which a cached response would
//     silently defeat the "exactly once" half of).
//   - Content-Security-Policy: default-src 'none' - the minimal, correct
//     policy for a pure JSON API that never itself serves executable
//     content; a browser that ever somehow renders one of these
//     responses as a document gets no script/style/image/frame
//     execution at all.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'")
		next.ServeHTTP(w, r)
	})
}

// PanicRecovery recovers a panicking handler, logs it (message and stack
// trace via slog), and returns a clean 500 - without this, an unhandled
// panic in ANY of this codebase's ~40 route-registering packages would
// crash the entire server process for every other in-flight request too,
// not just the one that panicked. Must be the OUTERMOST middleware in
// the chain (see Chain's own doc comment) so a panic in any
// later-in-the-chain middleware (RequestSizeLimit, RequestTimeout, or
// any route handler) is still caught - a panic that happened before this
// middleware even got a chance to run couldn't be recovered no matter
// where it were placed.
//
// If the panic happens after the handler has already written response
// headers/body, WriteHeader here is a documented no-op (net/http logs
// "superfluous WriteHeader call" but does not itself panic) - there is
// no way to un-send an already-flushed response, so this is the best a
// recovery middleware can do in that case; the panic is still logged
// either way.
func PanicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered", "requestId", RequestIDFromContext(r.Context()), "method", r.Method, "path", r.URL.Path,
					"panic", fmt.Sprintf("%v", rec), "stack", string(debug.Stack()))
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// RequestSizeLimit caps request body size via http.MaxBytesReader -
// maxUploadBytes for multipart/form-data (file uploads), maxJSONBytes
// for everything else (this batch's own design brief: "10MB for file
// uploads, 1MB for JSON"). A body that exceeds its limit fails with a
// clean read error the first time the handler's own json.Decoder (or
// equivalent) tries to read past the cap - http.MaxBytesReader's own
// documented behavior - rather than an unbounded body being read fully
// into memory first.
func RequestSizeLimit(maxJSONBytes, maxUploadBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit := maxJSONBytes
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				limit = maxUploadBytes
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// ssePathSuffix is the one route exempted from RequestTimeout - a
// long-lived Server-Sent-Events stream (internal/permission's own
// GET .../permission-events), which is SUPPOSED to stay open far longer
// than an ordinary request's timeout budget. A blanket per-request
// deadline would kill this stream every RequestTimeout duration, which
// is not what "request timeout" is meant to protect against here.
const ssePathSuffix = "/permission-events"

// RequestTimeout bounds how long a single request's own context.Context
// may run before it is cancelled - implemented as a plain
// context.WithTimeout wrapping the request's context, NOT
// http.TimeoutHandler: this codebase's handlers already thread ctx
// through every database call (pgx honors context cancellation/deadline
// natively), so a cancelled context surfaces as a real, ordinary error
// each handler's own error-mapping already knows how to turn into a
// clean response - no separate "second response path" the way
// http.TimeoutHandler's own detached-goroutine-plus-substitute-response
// model would introduce (and that model doesn't actually stop the
// original handler goroutine running when it fires, only the response
// it would have sent - the wrong shape for a hardening measure whose
// whole point is bounding resource usage, not just bounding what the
// client waits for).
//
// Exempts ssePathSuffix (see its own doc comment) by passing the
// request through with its context untouched.
func RequestTimeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, ssePathSuffix) {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Chain composes RequestID, SecurityHeaders, PanicRecovery,
// RequestSizeLimit, RequestTimeout, and rateLimit around next, in that
// specific order (outermost to innermost, i.e. the order requests pass
// through on the way IN) - this is the tenant-isolation-hardening/
// security batch's own audited final chain order (Part 3):
//
//		RequestID -> SecurityHeaders -> PanicRecovery -> RequestSizeLimit
//		  -> RequestTimeout -> RateLimit -> Auth -> Handler
//
//	 1. RequestID - every request gets an ID and a correlation log line,
//	    even one that later panics or times out; must run before
//	    PanicRecovery logs anything so that log line already carries the
//	    request ID.
//	 2. SecurityHeaders - set on every response regardless of what
//	    happens later in the chain (including a PanicRecovery-caught
//	    panic's own error response), so a security header is never
//	    accidentally skipped for an unusual response path.
//	 3. PanicRecovery - must be outside everything else it needs to
//	    protect (see its own doc comment for why outermost-after-
//	    RequestID/SecurityHeaders).
//	 4. RequestSizeLimit - reject an oversized body before any handler
//	    (or the timeout deadline's clock) does real work reading it.
//	 5. RequestTimeout - the route handler runs with a bounded context.
//	 6. rateLimit (internal/ratelimit.Middleware, passed in rather than
//	    imported directly - see this parameter's own doc note below) -
//	    innermost: runs immediately before internal/identity.Middleware
//	    (per-route "Auth", applied inside each module's own RegisterRoutes,
//	    inside the mux this Chain wraps) and the route handler itself.
//
// rateLimit may be nil, in which case this position in the chain is a
// pure pass-through (the same "wrap or no-op on nil" convention
// telemetry.Middleware already establishes for its own optional
// wrapping) - a caller that doesn't want rate limiting at all (e.g. a
// unit test building its own minimal chain) pays no cost for the
// feature existing. It is accepted as a parameter, not called via a
// direct import of internal/ratelimit, so this package - imported
// transitively by nearly every other package via cmd/server/main.go's
// own Chain call - stays exactly as dependency-free as its own package
// doc comment already promises; cmd/server/main.go is the one place
// that constructs a real internal/ratelimit.Limiter and passes
// ratelimit.Middleware(limiter) in.
//
// internal/identity.Middleware (per-route Keycloak auth, applied inside
// each module's own RegisterRoutes, before the mux this Chain wraps is
// even fully built) still runs AFTER every layer above on the way in -
// unauthenticated requests still get a request ID/security headers/
// panic recovery/size limit/timeout/rate limit, which is the right
// default (a malformed, oversized, or floodingly-frequent unauthenticated
// request should never be able to crash, hang, or overwhelm the server
// either).
func Chain(next http.Handler, requestTimeout time.Duration, maxJSONBytes, maxUploadBytes int64, rateLimit func(http.Handler) http.Handler) http.Handler {
	if rateLimit == nil {
		rateLimit = func(h http.Handler) http.Handler { return h }
	}
	return RequestID(SecurityHeaders(PanicRecovery(RequestSizeLimit(maxJSONBytes, maxUploadBytes)(RequestTimeout(requestTimeout)(rateLimit(APIVersionAlias(next)))))))
}

// apiV1Prefix is the versioned alias APIVersionAlias rewrites off every
// incoming request path before the mux ever sees it.
const apiV1Prefix = "/api/v1/"

// APIVersionAlias implements the mobile API optimization batch's own API
// versioning policy (Part 4, documented in docs/DEVELOPMENT.md): /api/v1/
// is an ADDITIVE alias for the existing, unversioned /api/ routes, not a
// second copy of every route registration. A request to
// /api/v1/firms/{firmID}/products is rewritten to /api/firms/{firmID}/products
// before reaching internal/platform/httpapi's mux, so it resolves to the
// exact same handler an unversioned caller already gets - every existing
// client (unversioned) keeps working unchanged, and a new client can opt
// into the /v1/ prefix today with zero behavior difference. Only when a
// FUTURE breaking change needs its own, genuinely different v2 route set
// would this alias stop being sufficient - see docs/DEVELOPMENT.md's own
// "breaking changes require a new version" policy.
func APIVersionAlias(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, apiV1Prefix) {
			next.ServeHTTP(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, apiV1Prefix)
		r2.URL.RawPath = ""
		next.ServeHTTP(w, r2)
	})
}
