// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package reports

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
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

// RegisterRoutes wires the reporting foundation's HTTP endpoints into
// mux, same bearer-token auth middleware and /api/firms/{firmID}/...
// convention as every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)
	mux.Handle("GET /api/firms/{firmID}/reports/kpis", auth(http.HandlerFunc(handleDashboardKPIs(pool))))

	mux.Handle("GET /api/firms/{firmID}/report-definitions", auth(http.HandlerFunc(handleListReportDefinitions(pool))))
	mux.Handle("POST /api/firms/{firmID}/report-definitions", auth(http.HandlerFunc(handleCreateReportDefinition(pool))))
	mux.Handle("GET /api/firms/{firmID}/report-definitions/{id}", auth(http.HandlerFunc(handleGetReportDefinition(pool))))
	mux.Handle("POST /api/firms/{firmID}/report-definitions/{id}/run", auth(http.HandlerFunc(handleRunReport(pool))))
	mux.Handle("GET /api/firms/{firmID}/reports/cohorts", auth(http.HandlerFunc(handleCohortAnalysis(pool))))
}

func writeReportsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrReportDefinitionNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrInvalidQuerySpec), errors.Is(err, ErrCohortSpecNotSupported):
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

type reportDefinitionResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	QuerySpec   QuerySpec `json:"querySpec"`
	CreatedBy   string    `json:"createdBy"`
	CreatedAt   string    `json:"createdAt"`
}

func toReportDefinitionResponse(d ReportDefinition) reportDefinitionResponse {
	return reportDefinitionResponse{
		ID: d.ID.String(), Name: d.Name, Description: d.Description, QuerySpec: d.QuerySpec,
		CreatedBy: d.CreatedBy.String(), CreatedAt: d.CreatedAt.Format(time.RFC3339),
	}
}

type reportDefinitionRequest struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	QuerySpec   QuerySpec `json:"querySpec"`
}

func handleListReportDefinitions(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		defs, err := ListReportDefinitions(r.Context(), pool, firmID, userID)
		if err != nil {
			writeReportsError(w, err)
			return
		}
		resp := make([]reportDefinitionResponse, 0, len(defs))
		for _, d := range defs {
			resp = append(resp, toReportDefinitionResponse(d))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleCreateReportDefinition(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req reportDefinitionRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		def, err := CreateReportDefinition(r.Context(), pool, firmID, userID, ReportDefinitionInput{
			Name: req.Name, Description: req.Description, QuerySpec: req.QuerySpec,
		})
		if err != nil {
			writeReportsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toReportDefinitionResponse(def))
	}
}

func handleGetReportDefinition(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		definitionID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid report definition id", http.StatusBadRequest)
			return
		}
		def, err := GetReportDefinition(r.Context(), pool, firmID, userID, definitionID)
		if err != nil {
			writeReportsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toReportDefinitionResponse(def))
	}
}

func handleRunReport(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		definitionID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid report definition id", http.StatusBadRequest)
			return
		}
		rows, err := RunReport(r.Context(), pool, firmID, userID, definitionID)
		if err != nil {
			writeReportsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rows)
	}
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

type cohortRowResponse struct {
	CohortMonth string     `json:"cohortMonth"`
	CohortSize  int        `json:"cohortSize"`
	Values      []*float64 `json:"values"`
}

type cohortTableResponse struct {
	Periods int                 `json:"periods"`
	Cohorts []cohortRowResponse `json:"cohorts"`
}

// handleCohortAnalysis accepts GET .../reports/cohorts?entity=customers&cohort_by=created_at&metric=invoice_total&periods=6
// - Part 3's own cohort analysis endpoint. See cohort.go's own doc
// comment for why only that one (entity, cohort_by, metric) combination
// is supported.
func handleCohortAnalysis(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		q := r.URL.Query()
		periods := 0
		if raw := q.Get("periods"); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				periods = n
			}
		}

		table, err := CohortAnalysis(r.Context(), pool, firmID, userID, CohortSpec{
			Entity: q.Get("entity"), CohortBy: q.Get("cohort_by"), Metric: q.Get("metric"), Periods: periods,
		})
		if err != nil {
			writeReportsError(w, err)
			return
		}

		resp := cohortTableResponse{Periods: table.Periods, Cohorts: make([]cohortRowResponse, 0, len(table.Cohorts))}
		for _, row := range table.Cohorts {
			resp.Cohorts = append(resp.Cohorts, cohortRowResponse{
				CohortMonth: row.CohortMonth.Format("2006-01"), CohortSize: row.CohortSize, Values: row.Values,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
