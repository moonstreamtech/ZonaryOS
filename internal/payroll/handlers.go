// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package payroll

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// decodeJSONBody mirrors internal/hr's own helper exactly - a missing/
// empty body is a no-op, not an error.
func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires payroll's HTTP endpoints into mux - same bearer-
// token auth middleware and /api/firms/{firmID}/... convention as every
// other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/payroll-periods", auth(http.HandlerFunc(handleListPeriods(pool))))
	mux.Handle("POST /api/firms/{firmID}/payroll-periods", auth(http.HandlerFunc(handleCreatePeriod(pool))))
	mux.Handle("GET /api/firms/{firmID}/payroll-periods/{periodID}", auth(http.HandlerFunc(handleGetPeriod(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/payroll-periods/{periodID}", auth(http.HandlerFunc(handleUpdatePeriod(pool))))
	mux.Handle("POST /api/firms/{firmID}/payroll-periods/{periodID}/close", auth(http.HandlerFunc(handleClosePeriod(pool))))
	mux.Handle("GET /api/firms/{firmID}/payroll-periods/{periodID}/entries", auth(http.HandlerFunc(handleListEntries(pool))))
	mux.Handle("POST /api/firms/{firmID}/payroll-periods/{periodID}/entries", auth(http.HandlerFunc(handleCreateEntry(pool))))
	mux.Handle("GET /api/firms/{firmID}/payroll-entries/{entryID}", auth(http.HandlerFunc(handleGetEntry(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/payroll-entries/{entryID}", auth(http.HandlerFunc(handleUpdateEntry(pool))))
}

// writePayrollError maps this package's sentinel errors to the HTTP
// status that reflects why the caller isn't getting what they asked for -
// same convention as internal/hr's writeHRError.
func writePayrollError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrPeriodNotFound), errors.Is(err, ErrEntryNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidPeriod), errors.Is(err, ErrInvalidEntry), errors.Is(err, ErrPeriodNotOpen):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrPeriodAlreadyClosed):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type periodResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	PeriodStart string `json:"periodStart"`
	PeriodEnd   string `json:"periodEnd"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
}

func toPeriodResponse(p Period) periodResponse {
	return periodResponse{
		ID:          p.ID.String(),
		Name:        p.Name,
		PeriodStart: p.PeriodStart.Format(dateLayout),
		PeriodEnd:   p.PeriodEnd.Format(dateLayout),
		Status:      string(p.Status),
		CreatedAt:   p.CreatedAt.Format(time.RFC3339),
	}
}

type entryResponse struct {
	ID          string         `json:"id"`
	PeriodID    string         `json:"periodId"`
	PersonID    string         `json:"personId"`
	ContractID  *string        `json:"contractId,omitempty"`
	GrossAmount string         `json:"grossAmount"`
	Deductions  map[string]any `json:"deductions"`
	NetAmount   string         `json:"netAmount"`
	Currency    string         `json:"currency"`
	Notes       *string        `json:"notes,omitempty"`
	CreatedAt   string         `json:"createdAt"`
}

func toEntryResponse(e Entry) entryResponse {
	var contractID *string
	if e.ContractID != nil {
		s := e.ContractID.String()
		contractID = &s
	}
	return entryResponse{
		ID: e.ID.String(), PeriodID: e.PeriodID.String(), PersonID: e.PersonID.String(), ContractID: contractID,
		GrossAmount: e.GrossAmount, Deductions: e.Deductions, NetAmount: e.NetAmount, Currency: e.Currency,
		Notes: e.Notes, CreatedAt: e.CreatedAt.Format(time.RFC3339),
	}
}

func resolveCaller(r *http.Request, pool *pgxpool.Pool) (firmID, userID uuid.UUID, ok bool) {
	id, idOK := identity.FromContext(r.Context())
	if !idOK {
		return uuid.UUID{}, uuid.UUID{}, false
	}
	firmID, err := uuid.Parse(r.PathValue("firmID"))
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, false
	}
	userID, err = identity.ResolveOrCreateUser(r.Context(), pool, id)
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, false
	}
	return firmID, userID, true
}

func handleListPeriods(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		periods, err := ListPeriods(r.Context(), pool, firmID, userID)
		if err != nil {
			writePayrollError(w, err)
			return
		}
		resp := make([]periodResponse, 0, len(periods))
		for _, p := range periods {
			resp = append(resp, toPeriodResponse(p))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createPeriodRequest struct {
	Name        string `json:"name"`
	PeriodStart string `json:"periodStart"`
	PeriodEnd   string `json:"periodEnd"`
}

func handleCreatePeriod(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		var req createPeriodRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		period, err := CreatePeriod(r.Context(), pool, firmID, userID, CreatePeriodInput{
			Name: req.Name, PeriodStart: req.PeriodStart, PeriodEnd: req.PeriodEnd,
		})
		if err != nil {
			writePayrollError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toPeriodResponse(period))
	}
}

func handleGetPeriod(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		periodID, err := uuid.Parse(r.PathValue("periodID"))
		if err != nil {
			http.Error(w, "invalid period id", http.StatusBadRequest)
			return
		}
		period, err := GetPeriod(r.Context(), pool, firmID, userID, periodID)
		if err != nil {
			writePayrollError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPeriodResponse(period))
	}
}

type updatePeriodRequest struct {
	Name        *string `json:"name"`
	PeriodStart *string `json:"periodStart"`
	PeriodEnd   *string `json:"periodEnd"`
}

func handleUpdatePeriod(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		periodID, err := uuid.Parse(r.PathValue("periodID"))
		if err != nil {
			http.Error(w, "invalid period id", http.StatusBadRequest)
			return
		}
		var req updatePeriodRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		period, err := UpdatePeriod(r.Context(), pool, firmID, userID, periodID, PeriodUpdate{
			Name: req.Name, PeriodStart: req.PeriodStart, PeriodEnd: req.PeriodEnd,
		})
		if err != nil {
			writePayrollError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPeriodResponse(period))
	}
}

func handleClosePeriod(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		periodID, err := uuid.Parse(r.PathValue("periodID"))
		if err != nil {
			http.Error(w, "invalid period id", http.StatusBadRequest)
			return
		}
		period, err := ClosePeriod(r.Context(), pool, firmID, userID, periodID)
		if err != nil {
			writePayrollError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPeriodResponse(period))
	}
}

func handleListEntries(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		periodID, err := uuid.Parse(r.PathValue("periodID"))
		if err != nil {
			http.Error(w, "invalid period id", http.StatusBadRequest)
			return
		}
		entries, err := ListEntries(r.Context(), pool, firmID, userID, periodID)
		if err != nil {
			writePayrollError(w, err)
			return
		}
		resp := make([]entryResponse, 0, len(entries))
		for _, e := range entries {
			resp = append(resp, toEntryResponse(e))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type entryRequest struct {
	PersonID    string         `json:"personId"`
	ContractID  string         `json:"contractId"`
	GrossAmount string         `json:"grossAmount"`
	Deductions  map[string]any `json:"deductions"`
	NetAmount   string         `json:"netAmount"`
	Currency    string         `json:"currency"`
	Notes       string         `json:"notes"`
}

func (req entryRequest) toInput() (EntryInput, error) {
	input := EntryInput{
		GrossAmount: req.GrossAmount, Deductions: req.Deductions, NetAmount: req.NetAmount,
		Currency: req.Currency, Notes: req.Notes,
	}
	if req.PersonID != "" {
		personID, err := uuid.Parse(req.PersonID)
		if err != nil {
			return EntryInput{}, err
		}
		input.PersonID = personID
	}
	if req.ContractID != "" {
		contractID, err := uuid.Parse(req.ContractID)
		if err != nil {
			return EntryInput{}, err
		}
		input.ContractID = &contractID
	}
	return input, nil
}

func handleCreateEntry(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		periodID, err := uuid.Parse(r.PathValue("periodID"))
		if err != nil {
			http.Error(w, "invalid period id", http.StatusBadRequest)
			return
		}
		var req entryRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		input, err := req.toInput()
		if err != nil {
			http.Error(w, "invalid person or contract id", http.StatusBadRequest)
			return
		}
		entry, err := CreateEntry(r.Context(), pool, firmID, userID, periodID, input)
		if err != nil {
			writePayrollError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toEntryResponse(entry))
	}
}

func handleGetEntry(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		entryID, err := uuid.Parse(r.PathValue("entryID"))
		if err != nil {
			http.Error(w, "invalid entry id", http.StatusBadRequest)
			return
		}
		entry, err := GetEntry(r.Context(), pool, firmID, userID, entryID)
		if err != nil {
			writePayrollError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toEntryResponse(entry))
	}
}

func handleUpdateEntry(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		entryID, err := uuid.Parse(r.PathValue("entryID"))
		if err != nil {
			http.Error(w, "invalid entry id", http.StatusBadRequest)
			return
		}
		var req entryRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		input, err := req.toInput()
		if err != nil {
			http.Error(w, "invalid person or contract id", http.StatusBadRequest)
			return
		}
		entry, err := UpdateEntry(r.Context(), pool, firmID, userID, entryID, input)
		if err != nil {
			writePayrollError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toEntryResponse(entry))
	}
}
