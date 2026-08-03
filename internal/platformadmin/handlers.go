// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package platformadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/license"
)

// RegisterRoutes wires this package's one HTTP endpoint into mux, behind
// the same bearer-token auth middleware every other route group uses
// (internal/identity.Middleware) - additive-only, a new path, no change to
// any existing route (Never-Violate Rule 6).
//
// lic is internal/license's gate (Open Points item 32) - see ListFirms's
// doc comment for why this endpoint was chosen as the one illustrative
// module gate this batch wires. May be nil (license.Verifier's methods
// are nil-safe and always allow), for any caller that doesn't want
// license enforcement wired in at all.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool, allow Allowlist, lic *license.Verifier) {
	auth := identity.Middleware(verifier)
	mux.Handle("GET /api/platform-admin/firms", auth(http.HandlerFunc(handleListFirms(pool, allow, lic))))
}

type firmMetadataResponse struct {
	FirmID                  string `json:"firmId"`
	Name                    string `json:"name"`
	CreatedAt               string `json:"createdAt"`
	WorkflowDefinitionCount int    `json:"workflowDefinitionCount"`
	MemberCount             int    `json:"memberCount"`
}

// handleListFirms checks the allowlist as the very first thing it does
// after reading the already-verified identity off the request context -
// before resolving that identity into ZonaryOS's own `users` table and
// before any other database access - so a non-allowlisted caller never
// causes a query to run. It's rejected with 404, not 403: this endpoint is
// meant to behave, for anyone off the allowlist, as if it doesn't exist at
// all (the same stronger posture the frontend route takes with
// next/navigation's notFound(), see web/src/app/[locale]/platform-admin -
// a 403 would confirm the route's existence to a caller who has no
// business knowing about it).
func handleListFirms(pool *pgxpool.Pool, allow Allowlist, lic *license.Verifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}

		if !allow.Contains(id.Email) {
			http.NotFound(w, r)
			return
		}

		firms, err := ListFirms(r.Context(), pool, allow, id, lic)
		if err != nil {
			if errors.Is(err, ErrNotPlatformAdmin) {
				// Can only happen if the allowlist changed between the
				// check above and this call, or ListFirms is ever called
				// from a different caller - still 404, same reasoning.
				http.NotFound(w, r)
				return
			}
			if errors.Is(err, license.ErrDenied) {
				// 503, not 401/403/404: this caller IS an authorized
				// platform admin (the allowlist check above already
				// passed) - what's failing is the installation's own
				// license state, a service-level condition, not
				// something about this particular caller's identity or
				// authorization. The error's own message (built by
				// internal/license.Verifier.Allow) already tells an
				// operator exactly what's wrong and what env var to fix.
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		resp := make([]firmMetadataResponse, 0, len(firms))
		for _, f := range firms {
			resp = append(resp, firmMetadataResponse{
				FirmID:                  f.FirmID.String(),
				Name:                    f.Name,
				CreatedAt:               f.CreatedAt.Format(time.RFC3339),
				WorkflowDefinitionCount: f.WorkflowDefinitionCount,
				MemberCount:             f.MemberCount,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
