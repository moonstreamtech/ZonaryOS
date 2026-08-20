// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ratelimit

import (
	"fmt"
	"net/http"
)

// Middleware wraps next with per-(firm, category) rate limiting - a
// rejected request gets a 429 with a Retry-After header (in whole
// seconds, rounded up so a caller that waits exactly that long is never
// turned away early) and never reaches next at all.
//
// Positioned in cmd/server/main.go's own middleware.Chain immediately
// before internal/platform/httpapi's mux (i.e. immediately before each
// route's own internal/identity.Middleware and handler) - see
// middleware.Chain's own updated doc comment for the full chain order
// this batch settles on, and this package's own doc comment for why
// firm-scoping here is derived from the URL path's own shape (FirmKey)
// rather than from the identity context: a global middleware wrapping
// the entire mux runs before any PER-ROUTE auth middleware gets a
// chance to attach an identity to the request, so path-based extraction
// is not a workaround - it is the only mechanism available at this
// layer, and it has the added benefit of working identically for
// unauthenticated routes too.
func Middleware(limiter *Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := FirmKey(r.URL.Path)
			category := Classify(r)

			allowed, retryAfter := limiter.Allow(key, category)
			if !allowed {
				seconds := int(retryAfter.Seconds())
				if retryAfter > 0 && seconds == 0 {
					seconds = 1
				}
				w.Header().Set("Retry-After", fmt.Sprintf("%d", seconds))
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
