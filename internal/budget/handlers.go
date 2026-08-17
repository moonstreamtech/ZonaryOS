// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package budget

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

func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires internal/budget's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/budgets", auth(http.HandlerFunc(handleListBudgets(pool))))
	mux.Handle("POST /api/firms/{firmID}/budgets", auth(http.HandlerFunc(handleCreateBudget(pool))))
	mux.Handle("GET /api/firms/{firmID}/budgets/{budgetID}", auth(http.HandlerFunc(handleGetBudget(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/budgets/{budgetID}", auth(http.HandlerFunc(handleUpdateBudget(pool))))
	mux.Handle("POST /api/firms/{firmID}/budgets/{budgetID}/approve", auth(http.HandlerFunc(handleApproveBudget(pool))))

	mux.Handle("GET /api/firms/{firmID}/budgets/{budgetID}/lines", auth(http.HandlerFunc(handleListBudgetLines(pool))))
	mux.Handle("POST /api/firms/{firmID}/budgets/{budgetID}/lines", auth(http.HandlerFunc(handleCreateBudgetLine(pool))))
}

func writeBudgetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrBudgetNotFound), errors.Is(err, ErrBudgetLineNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidBudget), errors.Is(err, ErrInvalidBudgetTransition),
		errors.Is(err, ErrInvalidBudgetLine), errors.Is(err, ErrDuplicateBudgetLineAccount):
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

func uuidString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

type budgetResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	PeriodType  string  `json:"periodType"`
	PeriodStart string  `json:"periodStart"`
	PeriodEnd   string  `json:"periodEnd"`
	Status      string  `json:"status"`
	Currency    string  `json:"currency"`
	Notes       *string `json:"notes,omitempty"`
	ApprovedBy  *string `json:"approvedBy,omitempty"`
	ApprovedAt  *string `json:"approvedAt,omitempty"`
	CreatedAt   string  `json:"createdAt"`
}

func toBudgetResponse(b Budget) budgetResponse {
	var approvedAt *string
	if b.ApprovedAt != nil {
		s := b.ApprovedAt.Format(time.RFC3339)
		approvedAt = &s
	}
	return budgetResponse{
		ID: b.ID.String(), Name: b.Name, PeriodType: b.PeriodType,
		PeriodStart: b.PeriodStart.Format("2006-01-02"), PeriodEnd: b.PeriodEnd.Format("2006-01-02"),
		Status: b.Status, Currency: b.Currency, Notes: b.Notes, ApprovedBy: uuidString(b.ApprovedBy),
		ApprovedAt: approvedAt, CreatedAt: b.CreatedAt.Format(time.RFC3339),
	}
}

func handleListBudgets(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		budgets, err := ListBudgets(r.Context(), pool, firmID, userID)
		if err != nil {
			writeBudgetError(w, err)
			return
		}
		resp := make([]budgetResponse, 0, len(budgets))
		for _, b := range budgets {
			resp = append(resp, toBudgetResponse(b))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type budgetRequest struct {
	Name        string `json:"name"`
	PeriodType  string `json:"periodType"`
	PeriodStart string `json:"periodStart"`
	PeriodEnd   string `json:"periodEnd"`
	Status      string `json:"status"`
	Currency    string `json:"currency"`
	Notes       string `json:"notes"`
}

func handleCreateBudget(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req budgetRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		b, err := CreateBudget(r.Context(), pool, firmID, userID, CreateBudgetInput{
			Name: req.Name, PeriodType: req.PeriodType, PeriodStart: req.PeriodStart, PeriodEnd: req.PeriodEnd,
			Status: req.Status, Currency: req.Currency, Notes: req.Notes,
		})
		if err != nil {
			writeBudgetError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toBudgetResponse(b))
	}
}

func handleGetBudget(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		budgetID, err := uuid.Parse(r.PathValue("budgetID"))
		if err != nil {
			http.Error(w, "invalid budget id", http.StatusBadRequest)
			return
		}
		b, err := GetBudget(r.Context(), pool, firmID, userID, budgetID)
		if err != nil {
			writeBudgetError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toBudgetResponse(b))
	}
}

type updateBudgetRequest struct {
	Name     *string `json:"name"`
	Status   *string `json:"status"`
	Notes    *string `json:"notes"`
	Currency *string `json:"currency"`
}

func handleUpdateBudget(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		budgetID, err := uuid.Parse(r.PathValue("budgetID"))
		if err != nil {
			http.Error(w, "invalid budget id", http.StatusBadRequest)
			return
		}
		var req updateBudgetRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		b, err := UpdateBudget(r.Context(), pool, firmID, userID, budgetID, UpdateBudgetInput{
			Name: req.Name, Status: req.Status, Notes: req.Notes, Currency: req.Currency,
		})
		if err != nil {
			writeBudgetError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toBudgetResponse(b))
	}
}

func handleApproveBudget(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		budgetID, err := uuid.Parse(r.PathValue("budgetID"))
		if err != nil {
			http.Error(w, "invalid budget id", http.StatusBadRequest)
			return
		}
		b, err := ApproveBudget(r.Context(), pool, firmID, userID, budgetID)
		if err != nil {
			writeBudgetError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toBudgetResponse(b))
	}
}

type budgetLineResponse struct {
	ID            string  `json:"id"`
	BudgetID      string  `json:"budgetId"`
	AccountID     string  `json:"accountId"`
	PlannedAmount string  `json:"plannedAmount"`
	Notes         *string `json:"notes,omitempty"`
}

func toBudgetLineResponse(l BudgetLine) budgetLineResponse {
	return budgetLineResponse{ID: l.ID.String(), BudgetID: l.BudgetID.String(), AccountID: l.AccountID.String(), PlannedAmount: l.PlannedAmount, Notes: l.Notes}
}

func handleListBudgetLines(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		budgetID, err := uuid.Parse(r.PathValue("budgetID"))
		if err != nil {
			http.Error(w, "invalid budget id", http.StatusBadRequest)
			return
		}
		lines, err := ListBudgetLines(r.Context(), pool, firmID, userID, budgetID)
		if err != nil {
			writeBudgetError(w, err)
			return
		}
		resp := make([]budgetLineResponse, 0, len(lines))
		for _, l := range lines {
			resp = append(resp, toBudgetLineResponse(l))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type budgetLineRequest struct {
	AccountID     uuid.UUID `json:"accountId"`
	PlannedAmount string    `json:"plannedAmount"`
	Notes         string    `json:"notes"`
}

func handleCreateBudgetLine(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		budgetID, err := uuid.Parse(r.PathValue("budgetID"))
		if err != nil {
			http.Error(w, "invalid budget id", http.StatusBadRequest)
			return
		}
		var req budgetLineRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		l, err := CreateBudgetLine(r.Context(), pool, firmID, userID, budgetID, CreateBudgetLineInput{
			AccountID: req.AccountID, PlannedAmount: req.PlannedAmount, Notes: req.Notes,
		})
		if err != nil {
			writeBudgetError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toBudgetLineResponse(l))
	}
}
