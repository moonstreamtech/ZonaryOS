// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package reports

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// RegisterRoutes wires the reporting foundation's one HTTP endpoint into
// mux, same bearer-token auth middleware and /api/firms/{firmID}/...
// convention as every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)
	mux.Handle("GET /api/firms/{firmID}/reports/kpis", auth(http.HandlerFunc(handleDashboardKPIs(pool))))
}

type kpiResponse struct {
	Key   string `json:"key"`
	Unit  string `json:"unit"`
	Value string `json:"value"`
}

// handleDashboardKPIs serves GET /api/firms/{firmID}/reports/kpis - the
// /reports page's data source.
func handleDashboardKPIs(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
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

		results, err := GetDashboardKPIs(r.Context(), pool, firmID, userID)
		if err != nil {
			if errors.Is(err, ErrFirmNotFound) {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		resp := make([]kpiResponse, 0, len(results))
		for _, r := range results {
			resp = append(resp, kpiResponse{Key: r.Key, Unit: r.Unit, Value: r.Value})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
