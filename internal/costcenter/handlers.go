// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package costcenter

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

// RegisterRoutes wires internal/costcenter's HTTP endpoints into mux,
// same bearer-token auth middleware and /api/firms/{firmID}/...
// convention as every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/cost-centers", auth(http.HandlerFunc(handleListCostCenters(pool))))
	mux.Handle("POST /api/firms/{firmID}/cost-centers", auth(http.HandlerFunc(handleCreateCostCenter(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/cost-centers/{costCenterID}", auth(http.HandlerFunc(handleUpdateCostCenter(pool))))

	mux.Handle("GET /api/firms/{firmID}/reports/cost-center-pnl", auth(http.HandlerFunc(handleCostCenterPnL(pool))))
}

func writeCostCenterError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrCostCenterNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidCostCenter), errors.Is(err, ErrDuplicateCode):
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

// parseReportDateRange reads ?from=/?to= off r as optional RFC3339
// timestamps - same convention internal/accounting's own
// parseReportDateRange uses for handlePnLReport.
func parseReportDateRange(r *http.Request) (from, to *time.Time, err error) {
	if raw := r.URL.Query().Get("from"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, nil, errors.New("invalid from (expected RFC3339)")
		}
		from = &t
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, nil, errors.New("invalid to (expected RFC3339)")
		}
		to = &t
	}
	return from, to, nil
}

type costCenterResponse struct {
	ID        string  `json:"id"`
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	ParentID  *string `json:"parentId,omitempty"`
	IsActive  bool    `json:"isActive"`
	CreatedAt string  `json:"createdAt"`
}

func toCostCenterResponse(c CostCenter) costCenterResponse {
	var parentID *string
	if c.ParentID != nil {
		s := c.ParentID.String()
		parentID = &s
	}
	return costCenterResponse{ID: c.ID.String(), Code: c.Code, Name: c.Name, ParentID: parentID, IsActive: c.IsActive, CreatedAt: c.CreatedAt.Format(time.RFC3339)}
}

func handleListCostCenters(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		centers, err := ListCostCenters(r.Context(), pool, firmID, userID)
		if err != nil {
			writeCostCenterError(w, err)
			return
		}
		resp := make([]costCenterResponse, 0, len(centers))
		for _, c := range centers {
			resp = append(resp, toCostCenterResponse(c))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type costCenterRequest struct {
	Code     string     `json:"code"`
	Name     string     `json:"name"`
	ParentID *uuid.UUID `json:"parentId"`
}

func handleCreateCostCenter(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req costCenterRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		c, err := CreateCostCenter(r.Context(), pool, firmID, userID, CreateCostCenterInput{Code: req.Code, Name: req.Name, ParentID: req.ParentID})
		if err != nil {
			writeCostCenterError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toCostCenterResponse(c))
	}
}

type updateCostCenterRequest struct {
	Name     *string `json:"name"`
	IsActive *bool   `json:"isActive"`
}

func handleUpdateCostCenter(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		costCenterID, err := uuid.Parse(r.PathValue("costCenterID"))
		if err != nil {
			http.Error(w, "invalid cost center id", http.StatusBadRequest)
			return
		}
		var req updateCostCenterRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		c, err := UpdateCostCenter(r.Context(), pool, firmID, userID, costCenterID, UpdateCostCenterInput{Name: req.Name, IsActive: req.IsActive})
		if err != nil {
			writeCostCenterError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toCostCenterResponse(c))
	}
}

type costCenterPnLLineResponse struct {
	CostCenterID *string `json:"costCenterId,omitempty"`
	Name         string  `json:"name"`
	Revenue      string  `json:"revenue"`
	Expenses     string  `json:"expenses"`
	Net          string  `json:"net"`
}

func handleCostCenterPnL(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		from, to, err := parseReportDateRange(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		lines, err := GetCostCenterPnL(r.Context(), pool, firmID, userID, from, to)
		if err != nil {
			writeCostCenterError(w, err)
			return
		}
		resp := make([]costCenterPnLLineResponse, 0, len(lines))
		for _, l := range lines {
			var id *string
			if l.CostCenterID != nil {
				s := l.CostCenterID.String()
				id = &s
			}
			resp = append(resp, costCenterPnLLineResponse{CostCenterID: id, Name: l.Name, Revenue: l.Revenue, Expenses: l.Expenses, Net: l.Net})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
