// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package firm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/localization"
)

// localeToCountryCode is a narrow, explicitly PROVISIONAL heuristic (Open
// Points item 24 stays open on a real per-firm country field) used only
// to look up a soft fiscal hint - a VATNumberLabel/tax_id-format warning
// and a fiscal-year-start default - from fiscal_country_configs. firms
// has no country column of its own, only default_locale (a UI language
// choice, migrations/0006's own comment: "expected to hold one of this
// app's own locales") and default_currency/tax_id (free text, no ISO
// country code either) - so default_locale is the only signal available
// today, and only where it unambiguously implies one country. "tr" maps
// to Turkey; "en" and "ar" are each spoken across several of this
// batch's own seeded countries (en: GB and US; ar: none seeded at all)
// with no basis to prefer one over another, so this deliberately returns
// "" (no hint) for those rather than guessing - a wrong guess here would
// silently validate a firm's tax ID against the wrong country's format,
// which is worse than the "no hint" case every caller already handles.
// The real fix is a dedicated country field on firms, which is a product
// decision beyond this batch's own narrow scope.
func localeToCountryCode(locale string) string {
	switch strings.ToLower(strings.TrimSpace(locale)) {
	case "tr":
		return "TR"
	default:
		return ""
	}
}

// RegisterRoutes wires this package's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... path
// convention as every other firm-scoped route group. GET is item 36's
// read counterpart to the PATCH that already lived at this path (item
// 4) - the settings page needs somewhere to read the current
// address/tax_id/default_locale/default_currency/logo_url values from
// before it can render an edit form for them; /api/me only ever carried
// firmName (internal/identity.handleMe).
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)
	mux.Handle("GET /api/firms/{firmID}", auth(http.HandlerFunc(handleGet(pool))))
	mux.Handle("PATCH /api/firms/{firmID}", auth(http.HandlerFunc(handleUpdate(pool))))
}

// writeError maps this package's sentinel errors to the HTTP status that
// reflects why the caller isn't getting what they asked for - same
// convention as internal/workflow's writeEngineError.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrPermissionDenied):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidName), errors.Is(err, ErrMetadataTooLong):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// updateRequest is PATCH /api/firms/{firmID}'s body: Name (required, as
// item 4 always required) plus item 36's five optional metadata fields,
// each a pointer so the JSON decoder itself gives us the three-way
// "absent" (nil, field omitted) / "clear" (pointer to "") / "set"
// (pointer to a non-empty value) distinction Update's UpdateFields needs
// - no separate presence-tracking required.
type updateRequest struct {
	Name            string  `json:"name"`
	Address         *string `json:"address,omitempty"`
	TaxID           *string `json:"taxId,omitempty"`
	DefaultLocale   *string `json:"defaultLocale,omitempty"`
	DefaultCurrency *string `json:"defaultCurrency,omitempty"`
	LogoURL         *string `json:"logoUrl,omitempty"`
}

type firmResponse struct {
	FirmID          string  `json:"firmId"`
	Name            string  `json:"name"`
	Address         *string `json:"address"`
	TaxID           *string `json:"taxId"`
	DefaultLocale   *string `json:"defaultLocale"`
	DefaultCurrency *string `json:"defaultCurrency"`
	LogoURL         *string `json:"logoUrl"`
	// VATNumberLabel/TaxIDWarning/FiscalYearStartMonth (multi-language
	// UI/localization depth/fiscal compliance batch, Part 3/4) are soft
	// fiscal hints derived from localeToCountryCode's own provisional
	// locale->country mapping, never persisted - all nil/omitted when no
	// country can be inferred from DefaultLocale, or when that country
	// hasn't been seeded into fiscal_country_configs.
	VATNumberLabel *string `json:"vatNumberLabel,omitempty"`
	// TaxIDWarning is set only when TaxID is non-empty AND fails the
	// resolved country's own vat_number_pattern - Part 3's own "soft
	// validation - warn if invalid pattern, don't block" contract, so its
	// presence never affects whether Update above already succeeded.
	TaxIDWarning         *string `json:"taxIdWarning,omitempty"`
	FiscalYearStartMonth *int    `json:"fiscalYearStartMonth,omitempty"`
}

// applyFiscalHints fills firmResponse's optional VATNumberLabel/
// TaxIDWarning/FiscalYearStartMonth fields from the fiscal_country_configs
// row (if any) that m's DefaultLocale maps to - see
// localeToCountryCode's own doc comment for why this is a best-effort
// hint, not authoritative country data. A lookup failure (no country
// inferred, or that country never seeded) is silently treated as "no
// hint" - this is display-only, additive information, never something
// that should turn a successful GET/PATCH into an error.
func applyFiscalHints(ctx context.Context, pool *pgxpool.Pool, m Metadata, resp *firmResponse) {
	if m.DefaultLocale == nil {
		return
	}
	countryCode := localeToCountryCode(*m.DefaultLocale)
	if countryCode == "" {
		return
	}
	cfg, err := localization.GetFiscalCountryConfig(ctx, pool, countryCode)
	if err != nil {
		return
	}

	label := cfg.VATNumberLabel
	resp.VATNumberLabel = &label
	month := cfg.FiscalYearStartMonth
	resp.FiscalYearStartMonth = &month

	if m.TaxID != nil && strings.TrimSpace(*m.TaxID) != "" && cfg.VATNumberPattern != nil {
		if !localization.ValidateVATNumber(*cfg.VATNumberPattern, *m.TaxID) {
			warning := "does not match the expected " + label + " format for " + countryCode
			resp.TaxIDWarning = &warning
		}
	}
}

// handleGet returns firmID's current name and item 36 metadata,
// member-gated - see Get's doc comment for why this is a looser gate
// than the owner-only PATCH below.
func handleGet(pool *pgxpool.Pool) http.HandlerFunc {
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

		m, err := Get(r.Context(), pool, firmID, userID)
		if err != nil {
			writeError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buildFirmResponse(r.Context(), pool, m))
	}
}

// buildFirmResponse converts m into the wire response, filling the
// optional fiscal-hint fields via applyFiscalHints - the one place both
// handleGet and handleUpdate build their response, so a GET right after a
// PATCH reflects the exact same hint-resolution logic.
func buildFirmResponse(ctx context.Context, pool *pgxpool.Pool, m Metadata) firmResponse {
	resp := firmResponse{
		FirmID:          m.FirmID.String(),
		Name:            m.Name,
		Address:         m.Address,
		TaxID:           m.TaxID,
		DefaultLocale:   m.DefaultLocale,
		DefaultCurrency: m.DefaultCurrency,
		LogoURL:         m.LogoURL,
	}
	applyFiscalHints(ctx, pool, m, &resp)
	return resp
}

// handleUpdate is item 4's original name-only mutation, extended by item
// 36 to also accept the same five optional metadata fields handleGet
// returns - still one owner-gated PATCH at the same route, not a second
// endpoint (see this package's doc comment).
func handleUpdate(pool *pgxpool.Pool) http.HandlerFunc {
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

		var req updateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		fields := UpdateFields{
			Address:         req.Address,
			TaxID:           req.TaxID,
			DefaultLocale:   req.DefaultLocale,
			DefaultCurrency: req.DefaultCurrency,
			LogoURL:         req.LogoURL,
		}
		if err := Update(r.Context(), pool, firmID, userID, req.Name, fields); err != nil {
			writeError(w, err)
			return
		}

		m, err := Get(r.Context(), pool, firmID, userID)
		if err != nil {
			writeError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buildFirmResponse(r.Context(), pool, m))
	}
}
