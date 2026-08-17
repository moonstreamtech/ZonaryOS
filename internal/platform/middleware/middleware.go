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
		slog.Info("request", "requestId", id, "method", r.Method, "path", r.URL.Path, "status", rec.status, "durationMs", time.Since(start).Milliseconds())
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

// Chain composes RequestID, PanicRecovery, RequestSizeLimit, and
// RequestTimeout around next, in that specific order (outermost to
// innermost, i.e. the order requests pass through on the way IN):
//
//  1. RequestID - every request gets an ID and a correlation log line,
//     even one that later panics or times out; must run before
//     PanicRecovery logs anything so that log line already carries the
//     request ID.
//  2. PanicRecovery - must be outside everything else it needs to
//     protect (see its own doc comment for why outermost-after-
//     RequestID).
//  3. RequestSizeLimit - reject an oversized body before any handler
//     (or the timeout deadline's clock) does real work reading it.
//  4. RequestTimeout - innermost: the actual route handler runs with a
//     bounded context.
//
// internal/identity.Middleware (per-route Keycloak auth, applied inside
// each module's own RegisterRoutes, before the mux this Chain wraps is
// even fully built) still runs AFTER all four of these on the way in -
// unauthenticated requests still get a request ID/panic recovery/size
// limit/timeout, which is the right default (a malformed or oversized
// unauthenticated request should never be able to crash or hang the
// server either).
func Chain(next http.Handler, requestTimeout time.Duration, maxJSONBytes, maxUploadBytes int64) http.Handler {
	return RequestID(PanicRecovery(RequestSizeLimit(maxJSONBytes, maxUploadBytes)(RequestTimeout(requestTimeout)(next))))
}
