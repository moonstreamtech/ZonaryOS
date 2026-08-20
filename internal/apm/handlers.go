// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apm

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
)

// summaryWindow is GET /api/platform-admin/metrics/summary's own fixed
// "last 24h" window (Part 1's own requirement for every field this
// endpoint returns) - not caller-configurable, the same fixed-window
// posture internal/health's own checkTimeout establishes for a
// different constant.
const summaryWindow = 24 * time.Hour

const topSlowestLimit = 10

// RegisterRoutes wires this package's one HTTP endpoint into mux -
// platform-admin-only, same allowlist-then-404 convention
// internal/platformadmin.RegisterRoutes/internal/currency.RegisterRoutes
// already establish.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool, allow platformadmin.Allowlist) {
	auth := identity.Middleware(verifier)
	mux.Handle("GET /api/platform-admin/metrics/summary", auth(http.HandlerFunc(handleSummary(pool, allow))))
}

type endpointStatsResponse struct {
	PathPattern  string  `json:"pathPattern"`
	P50Ms        float64 `json:"p50Ms"`
	P95Ms        float64 `json:"p95Ms"`
	P99Ms        float64 `json:"p99Ms"`
	ErrorRate    float64 `json:"errorRate"`
	RequestCount int64   `json:"requestCount"`
}

type hourlyVolumeResponse struct {
	Hour  string `json:"hour"`
	Count int64  `json:"count"`
}

type summaryResponse struct {
	Endpoints    []endpointStatsResponse `json:"endpoints"`
	TopSlowest   []endpointStatsResponse `json:"topSlowest"`
	VolumeByHour []hourlyVolumeResponse  `json:"volumeByHour"`
}

func toEndpointStatsResponse(stats []EndpointStats) []endpointStatsResponse {
	resp := make([]endpointStatsResponse, 0, len(stats))
	for _, s := range stats {
		resp = append(resp, endpointStatsResponse{
			PathPattern: s.PathPattern, P50Ms: s.P50Ms, P95Ms: s.P95Ms, P99Ms: s.P99Ms,
			ErrorRate: s.ErrorRate, RequestCount: s.RequestCount,
		})
	}
	return resp
}

// handleSummary checks the allowlist first, before any database access
// - same posture internal/platformadmin.handleListFirms' own doc
// comment establishes, and the same 404 (not 403) rejection for a
// non-admin caller.
func handleSummary(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
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

		since := time.Now().UTC().Add(-summaryWindow)

		endpoints, err := EndpointStatsSince(r.Context(), pool, since)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		topSlowest, err := TopSlowestEndpoints(r.Context(), pool, since, topSlowestLimit)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		volume, err := VolumeByHourSince(r.Context(), pool, since)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		volumeResp := make([]hourlyVolumeResponse, 0, len(volume))
		for _, v := range volume {
			volumeResp = append(volumeResp, hourlyVolumeResponse{Hour: v.Hour.Format(time.RFC3339), Count: v.Count})
		}

		resp := summaryResponse{
			Endpoints:    toEndpointStatsResponse(endpoints),
			TopSlowest:   toEndpointStatsResponse(topSlowest),
			VolumeByHour: volumeResp,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
