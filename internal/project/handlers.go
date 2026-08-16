// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package project

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

// RegisterRoutes wires internal/project's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/projects", auth(http.HandlerFunc(handleListProjects(pool))))
	mux.Handle("POST /api/firms/{firmID}/projects", auth(http.HandlerFunc(handleCreateProject(pool))))
	mux.Handle("GET /api/firms/{firmID}/projects/{projectID}", auth(http.HandlerFunc(handleGetProject(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/projects/{projectID}", auth(http.HandlerFunc(handleUpdateProject(pool))))
	mux.Handle("GET /api/firms/{firmID}/projects/{projectID}/summary", auth(http.HandlerFunc(handleGetProjectSummary(pool))))
}

func writeProjectError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrProjectNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidProject):
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

type projectResponse struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Description     *string `json:"description,omitempty"`
	Status          string  `json:"status"`
	StartDate       *string `json:"startDate,omitempty"`
	EndDate         *string `json:"endDate,omitempty"`
	Budget          *string `json:"budget,omitempty"`
	Currency        string  `json:"currency"`
	ManagerPersonID *string `json:"managerPersonId,omitempty"`
	CreatedAt       string  `json:"createdAt"`
}

func formatDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

func toProjectResponse(p Project) projectResponse {
	var managerID *string
	if p.ManagerPersonID != nil {
		s := p.ManagerPersonID.String()
		managerID = &s
	}
	return projectResponse{
		ID: p.ID.String(), Name: p.Name, Description: p.Description, Status: string(p.Status),
		StartDate: formatDate(p.StartDate), EndDate: formatDate(p.EndDate), Budget: p.Budget,
		Currency: p.Currency, ManagerPersonID: managerID, CreatedAt: p.CreatedAt.Format(time.RFC3339),
	}
}

func handleListProjects(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		opts := ListProjectsOptions{Status: ProjectStatus(r.URL.Query().Get("status"))}
		projects, err := ListProjects(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeProjectError(w, err)
			return
		}
		resp := make([]projectResponse, 0, len(projects))
		for _, p := range projects {
			resp = append(resp, toProjectResponse(p))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createProjectRequest struct {
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	StartDate       string     `json:"startDate"`
	EndDate         string     `json:"endDate"`
	Budget          string     `json:"budget"`
	Currency        string     `json:"currency"`
	ManagerPersonID *uuid.UUID `json:"managerPersonId"`
}

func handleCreateProject(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createProjectRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		p, err := CreateProject(r.Context(), pool, firmID, userID, CreateProjectInput{
			Name: req.Name, Description: req.Description, StartDate: req.StartDate, EndDate: req.EndDate,
			Budget: req.Budget, Currency: req.Currency, ManagerPersonID: req.ManagerPersonID,
		})
		if err != nil {
			writeProjectError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toProjectResponse(p))
	}
}

func handleGetProject(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		projectID, err := uuid.Parse(r.PathValue("projectID"))
		if err != nil {
			http.Error(w, "invalid project id", http.StatusBadRequest)
			return
		}
		p, err := GetProject(r.Context(), pool, firmID, userID, projectID)
		if err != nil {
			writeProjectError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toProjectResponse(p))
	}
}

type updateProjectRequest struct {
	Name            *string    `json:"name"`
	Description     *string    `json:"description"`
	Status          *string    `json:"status"`
	StartDate       *string    `json:"startDate"`
	EndDate         *string    `json:"endDate"`
	Budget          *string    `json:"budget"`
	ManagerPersonID *uuid.UUID `json:"managerPersonId"`
}

func handleUpdateProject(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		projectID, err := uuid.Parse(r.PathValue("projectID"))
		if err != nil {
			http.Error(w, "invalid project id", http.StatusBadRequest)
			return
		}
		var req updateProjectRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		var projectStatus *ProjectStatus
		if req.Status != nil {
			s := ProjectStatus(*req.Status)
			projectStatus = &s
		}
		p, err := UpdateProject(r.Context(), pool, firmID, userID, projectID, UpdateProjectInput{
			Name: req.Name, Description: req.Description, Status: projectStatus, StartDate: req.StartDate,
			EndDate: req.EndDate, Budget: req.Budget, ManagerPersonID: req.ManagerPersonID,
		})
		if err != nil {
			writeProjectError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toProjectResponse(p))
	}
}

type taskStateCountResponse struct {
	StateKey string `json:"stateKey"`
	Count    int    `json:"count"`
}

type summaryResponse struct {
	TasksByState     []taskStateCountResponse `json:"tasksByState"`
	TotalTasks       int                      `json:"totalTasks"`
	TotalHoursLogged string                   `json:"totalHoursLogged"`
	Budget           *string                  `json:"budget,omitempty"`
	EstimatedCost    *string                  `json:"estimatedCost,omitempty"`
}

func handleGetProjectSummary(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		projectID, err := uuid.Parse(r.PathValue("projectID"))
		if err != nil {
			http.Error(w, "invalid project id", http.StatusBadRequest)
			return
		}
		s, err := GetProjectSummary(r.Context(), pool, firmID, userID, projectID)
		if err != nil {
			writeProjectError(w, err)
			return
		}
		resp := summaryResponse{TotalTasks: s.TotalTasks, TotalHoursLogged: s.TotalHoursLogged, Budget: s.Budget, EstimatedCost: s.EstimatedCost}
		resp.TasksByState = make([]taskStateCountResponse, 0, len(s.TasksByState))
		for _, c := range s.TasksByState {
			resp.TasksByState = append(resp.TasksByState, taskStateCountResponse{StateKey: c.StateKey, Count: c.Count})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
