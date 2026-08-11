// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package search

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/crm"
	"github.com/moonstreamtech/ZonaryOS/internal/hr"
	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
)

// RegisterRoutes wires this package's one HTTP endpoint into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)
	mux.Handle("GET /api/firms/{firmID}/search", auth(http.HandlerFunc(handleSearch(pool))))
}

type hitResponse struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
}

func toHitResponse(h Hit) hitResponse {
	return hitResponse{Type: h.Type, ID: h.ID.String(), Title: h.Title, Subtitle: h.Subtitle}
}

// handleSearch implements GET /api/firms/{firmID}/search?q=&types=a,b,c -
// types defaults to every type in AllTypes when omitted, so a caller
// wanting "search everything" doesn't have to spell out the full list.
func handleSearch(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, present := identity.FromContext(r.Context())
		if !present {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		firmID, err := uuid.Parse(r.PathValue("firmID"))
		if err != nil {
			http.Error(w, "invalid firm id", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		q := strings.TrimSpace(r.URL.Query().Get("q"))
		types := AllTypes
		if raw := r.URL.Query().Get("types"); raw != "" {
			types = strings.Split(raw, ",")
			for i, t := range types {
				types[i] = strings.TrimSpace(t)
			}
		}

		hits, err := Search(r.Context(), pool, firmID, userID, q, types)
		if err != nil {
			switch {
			case errors.Is(err, ErrInvalidType):
				http.Error(w, err.Error(), http.StatusBadRequest)
			// Each underlying entity package's own ErrFirmNotFound - not
			// a single shared sentinel across packages, so all three are
			// checked; whichever type(s) were requested, a non-member
			// caller fails the same permission.IsMember check in every
			// one of them.
			case errors.Is(err, inventory.ErrFirmNotFound), errors.Is(err, crm.ErrFirmNotFound), errors.Is(err, hr.ErrFirmNotFound):
				http.Error(w, "firm not found", http.StatusNotFound)
			default:
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}

		resp := make([]hitResponse, 0, len(hits))
		for _, h := range hits {
			resp = append(resp, toHitResponse(h))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
