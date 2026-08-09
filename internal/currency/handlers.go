// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package currency

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
)

// RegisterRoutes wires this package's two HTTP endpoints into mux.
// POST /api/exchange-rates is platform-admin-only (Keycloak-authenticated,
// then allowlist-checked, same convention internal/platformadmin's own
// handleListFirms establishes) - not firm-scoped, since exchange rates
// are platform-wide. GET /api/exchange-rates is deliberately
// unauthenticated: exchange rates carry no sensitive data, and a firm's
// own frontend (or any other caller) needs to read them without first
// having a bearer token for this specifically.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool, allow platformadmin.Allowlist) {
	auth := identity.Middleware(verifier)
	mux.Handle("POST /api/exchange-rates", auth(http.HandlerFunc(handleCreateRate(pool, allow))))
	mux.HandleFunc("GET /api/exchange-rates", handleListRates(pool))
}

func writeCurrencyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRate):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrRateNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type rateResponse struct {
	ID           string  `json:"id"`
	FromCurrency string  `json:"fromCurrency"`
	ToCurrency   string  `json:"toCurrency"`
	Rate         string  `json:"rate"`
	ValidFrom    string  `json:"validFrom"`
	ValidTo      *string `json:"validTo,omitempty"`
	Source       string  `json:"source,omitempty"`
	CreatedAt    string  `json:"createdAt"`
}

func toRateResponse(r Rate) rateResponse {
	resp := rateResponse{
		ID: r.ID.String(), FromCurrency: r.FromCurrency, ToCurrency: r.ToCurrency, Rate: r.Rate,
		ValidFrom: r.ValidFrom.Format(time.RFC3339), Source: r.Source, CreatedAt: r.CreatedAt.Format(time.RFC3339),
	}
	if r.ValidTo != nil {
		s := r.ValidTo.Format(time.RFC3339)
		resp.ValidTo = &s
	}
	return resp
}

type createRateRequest struct {
	FromCurrency string  `json:"fromCurrency"`
	ToCurrency   string  `json:"toCurrency"`
	Rate         string  `json:"rate"`
	ValidFrom    string  `json:"validFrom"`
	ValidTo      *string `json:"validTo,omitempty"`
	Source       string  `json:"source,omitempty"`
}

// handleCreateRate checks the allowlist as the very first thing it does
// after reading the already-verified identity off the request context -
// before any database access - same "404, not 403, off-allowlist looks
// like the route doesn't exist" posture
// internal/platformadmin.handleListFirms establishes.
func handleCreateRate(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
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

		var req createRateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		validFrom, err := time.Parse(time.RFC3339, req.ValidFrom)
		if err != nil {
			http.Error(w, "validFrom must be an RFC3339 timestamp", http.StatusBadRequest)
			return
		}
		var validTo *time.Time
		if req.ValidTo != nil && *req.ValidTo != "" {
			t, err := time.Parse(time.RFC3339, *req.ValidTo)
			if err != nil {
				http.Error(w, "validTo must be an RFC3339 timestamp", http.StatusBadRequest)
				return
			}
			validTo = &t
		}

		rate, err := CreateRate(r.Context(), pool, CreateRateInput{
			FromCurrency: req.FromCurrency, ToCurrency: req.ToCurrency, Rate: req.Rate,
			ValidFrom: validFrom, ValidTo: validTo, Source: req.Source,
		})
		if err != nil {
			writeCurrencyError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toRateResponse(rate))
	}
}

// handleListRates serves GET /api/exchange-rates?from=&to=&at= -
// unauthenticated (see RegisterRoutes's own doc comment). from/to/at are
// all optional; at (if present) must be RFC3339.
func handleListRates(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		opts := ListRatesOptions{From: q.Get("from"), To: q.Get("to")}
		if atParam := q.Get("at"); atParam != "" {
			at, err := time.Parse(time.RFC3339, atParam)
			if err != nil {
				http.Error(w, "at must be an RFC3339 timestamp", http.StatusBadRequest)
				return
			}
			opts.At = &at
		}

		rates, err := ListRates(r.Context(), pool, opts)
		if err != nil {
			writeCurrencyError(w, err)
			return
		}

		resp := make([]rateResponse, 0, len(rates))
		for _, rt := range rates {
			resp = append(resp, toRateResponse(rt))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
