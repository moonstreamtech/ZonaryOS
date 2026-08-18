// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package localization

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
)

func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires localization's HTTP endpoints into mux. The
// address/tax-rate routes are the ordinary Keycloak bearer-token chain,
// same convention as every other firm-scoped route group in this
// codebase. The fiscal-country-config routes (Part 3 of the multi-
// language UI/localization depth/fiscal compliance batch) are platform-
// wide reference data, not firm-scoped: GET is deliberately
// unauthenticated (same posture as internal/currency's own
// GET /api/exchange-rates - a country's VAT-number format/label/currency
// carries no sensitive data), and the writes are platform-admin-gated via
// allow, the same allowlist-checked-first-thing convention
// internal/currency.handleCreateRate already establishes.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool, allow platformadmin.Allowlist) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/addresses", auth(http.HandlerFunc(handleListAddresses(pool))))
	mux.Handle("POST /api/firms/{firmID}/addresses", auth(http.HandlerFunc(handleCreateAddress(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/addresses/{addressID}", auth(http.HandlerFunc(handleUpdateAddress(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/addresses/{addressID}", auth(http.HandlerFunc(handleDeleteAddress(pool))))

	mux.Handle("GET /api/firms/{firmID}/tax-rates", auth(http.HandlerFunc(handleListTaxRates(pool))))
	mux.Handle("POST /api/firms/{firmID}/tax-rates", auth(http.HandlerFunc(handleCreateTaxRate(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/tax-rates/{taxRateID}", auth(http.HandlerFunc(handleUpdateTaxRate(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/tax-rates/{taxRateID}", auth(http.HandlerFunc(handleDeleteTaxRate(pool))))

	mux.HandleFunc("GET /api/fiscal-country-configs", handleListFiscalCountryConfigs(pool))
	mux.HandleFunc("GET /api/fiscal-country-configs/{countryCode}", handleGetFiscalCountryConfig(pool))
	mux.Handle("POST /api/fiscal-country-configs", auth(http.HandlerFunc(handleUpsertFiscalCountryConfig(pool, allow))))
	mux.Handle("DELETE /api/fiscal-country-configs/{countryCode}", auth(http.HandlerFunc(handleDeleteFiscalCountryConfig(pool, allow))))
}

func writeLocalizationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrAddressNotFound), errors.Is(err, ErrTaxRateNotFound),
		errors.Is(err, ErrFiscalCountryConfigNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidAddress), errors.Is(err, ErrInvalidTaxRate), errors.Is(err, ErrInvalidFiscalCountryConfig):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func resolveIdentity(r *http.Request, pool *pgxpool.Pool) (firmID, userID uuid.UUID, ok bool, status int, msg string) {
	id, present := identity.FromContext(r.Context())
	if !present {
		return uuid.UUID{}, uuid.UUID{}, false, http.StatusInternalServerError, "missing identity"
	}
	firmID, err := uuid.Parse(r.PathValue("firmID"))
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, false, http.StatusBadRequest, "invalid firm id"
	}
	userID, err = identity.ResolveOrCreateUser(r.Context(), pool, id)
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, false, http.StatusInternalServerError, "failed to resolve user"
	}
	return firmID, userID, true, 0, ""
}

type addressResponse struct {
	ID            string  `json:"id"`
	Label         *string `json:"label,omitempty"`
	Line1         string  `json:"line1"`
	Line2         *string `json:"line2,omitempty"`
	City          string  `json:"city"`
	StateProvince *string `json:"stateProvince,omitempty"`
	PostalCode    *string `json:"postalCode,omitempty"`
	CountryCode   string  `json:"countryCode"`
	IsDefault     bool    `json:"isDefault"`
	CreatedAt     string  `json:"createdAt"`
}

func toAddressResponse(a Address) addressResponse {
	return addressResponse{
		ID: a.ID.String(), Label: a.Label, Line1: a.Line1, Line2: a.Line2, City: a.City,
		StateProvince: a.StateProvince, PostalCode: a.PostalCode, CountryCode: a.CountryCode,
		IsDefault: a.IsDefault, CreatedAt: a.CreatedAt.Format(time.RFC3339),
	}
}

type addressRequest struct {
	Label         string `json:"label"`
	Line1         string `json:"line1"`
	Line2         string `json:"line2"`
	City          string `json:"city"`
	StateProvince string `json:"stateProvince"`
	PostalCode    string `json:"postalCode"`
	CountryCode   string `json:"countryCode"`
	IsDefault     bool   `json:"isDefault"`
}

func (req addressRequest) toInput() AddressInput {
	return AddressInput{
		Label: req.Label, Line1: req.Line1, Line2: req.Line2, City: req.City,
		StateProvince: req.StateProvince, PostalCode: req.PostalCode, CountryCode: req.CountryCode, IsDefault: req.IsDefault,
	}
}

func handleListAddresses(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		addresses, err := ListAddresses(r.Context(), pool, firmID, userID)
		if err != nil {
			writeLocalizationError(w, err)
			return
		}
		resp := make([]addressResponse, 0, len(addresses))
		for _, a := range addresses {
			resp = append(resp, toAddressResponse(a))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleCreateAddress(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req addressRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		addr, err := CreateAddress(r.Context(), pool, firmID, userID, req.toInput())
		if err != nil {
			writeLocalizationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toAddressResponse(addr))
	}
}

func handleUpdateAddress(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		addressID, err := uuid.Parse(r.PathValue("addressID"))
		if err != nil {
			http.Error(w, "invalid address id", http.StatusBadRequest)
			return
		}
		var req addressRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		addr, err := UpdateAddress(r.Context(), pool, firmID, userID, addressID, req.toInput())
		if err != nil {
			writeLocalizationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAddressResponse(addr))
	}
}

func handleDeleteAddress(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		addressID, err := uuid.Parse(r.PathValue("addressID"))
		if err != nil {
			http.Error(w, "invalid address id", http.StatusBadRequest)
			return
		}
		if err := DeleteAddress(r.Context(), pool, firmID, userID, addressID); err != nil {
			writeLocalizationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type taxRateResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Rate        string  `json:"rate"`
	CountryCode *string `json:"countryCode,omitempty"`
	Region      *string `json:"region,omitempty"`
	AppliesTo   *string `json:"appliesTo,omitempty"`
	IsDefault   bool    `json:"isDefault"`
	CreatedAt   string  `json:"createdAt"`
}

func toTaxRateResponse(t TaxRate) taxRateResponse {
	return taxRateResponse{
		ID: t.ID.String(), Name: t.Name, Rate: t.Rate, CountryCode: t.CountryCode,
		Region: t.Region, AppliesTo: t.AppliesTo, IsDefault: t.IsDefault, CreatedAt: t.CreatedAt.Format(time.RFC3339),
	}
}

type taxRateRequest struct {
	Name        string `json:"name"`
	Rate        string `json:"rate"`
	CountryCode string `json:"countryCode"`
	Region      string `json:"region"`
	AppliesTo   string `json:"appliesTo"`
	IsDefault   bool   `json:"isDefault"`
}

func (req taxRateRequest) toInput() TaxRateInput {
	return TaxRateInput{
		Name: req.Name, Rate: req.Rate, CountryCode: req.CountryCode, Region: req.Region,
		AppliesTo: req.AppliesTo, IsDefault: req.IsDefault,
	}
}

func handleListTaxRates(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		rates, err := ListTaxRates(r.Context(), pool, firmID, userID)
		if err != nil {
			writeLocalizationError(w, err)
			return
		}
		resp := make([]taxRateResponse, 0, len(rates))
		for _, t := range rates {
			resp = append(resp, toTaxRateResponse(t))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleCreateTaxRate(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req taxRateRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		rate, err := CreateTaxRate(r.Context(), pool, firmID, userID, req.toInput())
		if err != nil {
			writeLocalizationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toTaxRateResponse(rate))
	}
}

func handleUpdateTaxRate(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		taxRateID, err := uuid.Parse(r.PathValue("taxRateID"))
		if err != nil {
			http.Error(w, "invalid tax rate id", http.StatusBadRequest)
			return
		}
		var req taxRateRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		rate, err := UpdateTaxRate(r.Context(), pool, firmID, userID, taxRateID, req.toInput())
		if err != nil {
			writeLocalizationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toTaxRateResponse(rate))
	}
}

func handleDeleteTaxRate(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		taxRateID, err := uuid.Parse(r.PathValue("taxRateID"))
		if err != nil {
			http.Error(w, "invalid tax rate id", http.StatusBadRequest)
			return
		}
		if err := DeleteTaxRate(r.Context(), pool, firmID, userID, taxRateID); err != nil {
			writeLocalizationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type fiscalCountryConfigResponse struct {
	CountryCode          string  `json:"countryCode"`
	VATNumberPattern     *string `json:"vatNumberPattern,omitempty"`
	VATNumberLabel       string  `json:"vatNumberLabel"`
	CurrencyCode         string  `json:"currencyCode"`
	DateFormat           string  `json:"dateFormat"`
	FiscalYearStartMonth int     `json:"fiscalYearStartMonth"`
}

func toFiscalCountryConfigResponse(c FiscalCountryConfig) fiscalCountryConfigResponse {
	return fiscalCountryConfigResponse{
		CountryCode: c.CountryCode, VATNumberPattern: c.VATNumberPattern, VATNumberLabel: c.VATNumberLabel,
		CurrencyCode: c.CurrencyCode, DateFormat: c.DateFormat, FiscalYearStartMonth: c.FiscalYearStartMonth,
	}
}

type fiscalCountryConfigRequest struct {
	CountryCode          string `json:"countryCode"`
	VATNumberPattern     string `json:"vatNumberPattern"`
	VATNumberLabel       string `json:"vatNumberLabel"`
	CurrencyCode         string `json:"currencyCode"`
	DateFormat           string `json:"dateFormat"`
	FiscalYearStartMonth int    `json:"fiscalYearStartMonth"`
}

// handleListFiscalCountryConfigs serves GET /api/fiscal-country-configs -
// unauthenticated, see RegisterRoutes's own doc comment.
func handleListFiscalCountryConfigs(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		configs, err := ListFiscalCountryConfigs(r.Context(), pool)
		if err != nil {
			writeLocalizationError(w, err)
			return
		}
		resp := make([]fiscalCountryConfigResponse, 0, len(configs))
		for _, c := range configs {
			resp = append(resp, toFiscalCountryConfigResponse(c))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// handleGetFiscalCountryConfig serves
// GET /api/fiscal-country-configs/{countryCode} - unauthenticated, same
// posture as handleListFiscalCountryConfigs.
func handleGetFiscalCountryConfig(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := GetFiscalCountryConfig(r.Context(), pool, r.PathValue("countryCode"))
		if err != nil {
			writeLocalizationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toFiscalCountryConfigResponse(c))
	}
}

// handleUpsertFiscalCountryConfig serves POST /api/fiscal-country-configs -
// platform-admin-gated, checked as the very first thing after reading the
// already-verified identity off the request context, before any database
// access - same "404, not 403, off-allowlist looks like the route doesn't
// exist" posture internal/currency.handleCreateRate establishes.
func handleUpsertFiscalCountryConfig(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
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

		var req fiscalCountryConfigRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		c, err := UpsertFiscalCountryConfig(r.Context(), pool, FiscalCountryConfigInput{
			CountryCode: req.CountryCode, VATNumberPattern: req.VATNumberPattern, VATNumberLabel: req.VATNumberLabel,
			CurrencyCode: req.CurrencyCode, DateFormat: req.DateFormat, FiscalYearStartMonth: req.FiscalYearStartMonth,
		})
		if err != nil {
			writeLocalizationError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toFiscalCountryConfigResponse(c))
	}
}

// handleDeleteFiscalCountryConfig serves
// DELETE /api/fiscal-country-configs/{countryCode} - platform-admin-gated,
// same posture as handleUpsertFiscalCountryConfig.
func handleDeleteFiscalCountryConfig(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
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

		if err := DeleteFiscalCountryConfig(r.Context(), pool, r.PathValue("countryCode")); err != nil {
			writeLocalizationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
