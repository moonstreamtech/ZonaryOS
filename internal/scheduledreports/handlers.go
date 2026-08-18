// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package scheduledreports

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

func writeScheduledReportsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrScheduledReportNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidScheduledReport):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// RegisterRoutes wires this package's owner-gated CRUD for scheduled_reports.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)
	mux.Handle("GET /api/firms/{firmID}/scheduled-reports", auth(http.HandlerFunc(handleListScheduledReports(pool))))
	mux.Handle("POST /api/firms/{firmID}/scheduled-reports", auth(http.HandlerFunc(handleCreateScheduledReport(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/scheduled-reports/{id}", auth(http.HandlerFunc(handleUpdateScheduledReport(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/scheduled-reports/{id}", auth(http.HandlerFunc(handleDeleteScheduledReport(pool))))
}

type scheduledReportResponse struct {
	ID               string   `json:"id"`
	DefinitionID     string   `json:"definitionId"`
	Name             string   `json:"name"`
	ScheduleInterval string   `json:"scheduleInterval"`
	LastRunAt        *string  `json:"lastRunAt,omitempty"`
	NextRunAt        *string  `json:"nextRunAt,omitempty"`
	RecipientUserIDs []string `json:"recipientUserIds"`
	IsActive         bool     `json:"isActive"`
	CreatedAt        string   `json:"createdAt"`
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

func toScheduledReportResponse(s ScheduledReport) scheduledReportResponse {
	recipients := make([]string, 0, len(s.RecipientUserIDs))
	for _, id := range s.RecipientUserIDs {
		recipients = append(recipients, id.String())
	}
	return scheduledReportResponse{
		ID: s.ID.String(), DefinitionID: s.DefinitionID.String(), Name: s.Name,
		ScheduleInterval: string(s.ScheduleInterval), LastRunAt: formatTimePtr(s.LastRunAt), NextRunAt: formatTimePtr(s.NextRunAt),
		RecipientUserIDs: recipients, IsActive: s.IsActive, CreatedAt: s.CreatedAt.Format(time.RFC3339),
	}
}

type scheduledReportRequest struct {
	DefinitionID     string   `json:"definitionId"`
	Name             string   `json:"name"`
	ScheduleInterval string   `json:"scheduleInterval"`
	RecipientUserIDs []string `json:"recipientUserIds"`
	IsActive         bool     `json:"isActive"`
}

func (r scheduledReportRequest) toInput() (ScheduledReportInput, error) {
	definitionID, err := uuid.Parse(r.DefinitionID)
	if err != nil {
		return ScheduledReportInput{}, err
	}
	recipients := make([]uuid.UUID, 0, len(r.RecipientUserIDs))
	for _, raw := range r.RecipientUserIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return ScheduledReportInput{}, err
		}
		recipients = append(recipients, id)
	}
	return ScheduledReportInput{
		DefinitionID: definitionID, Name: r.Name, ScheduleInterval: ScheduleInterval(r.ScheduleInterval),
		RecipientUserIDs: recipients, IsActive: r.IsActive,
	}, nil
}

func handleListScheduledReports(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		list, err := ListScheduledReports(r.Context(), pool, firmID, userID)
		if err != nil {
			writeScheduledReportsError(w, err)
			return
		}
		resp := make([]scheduledReportResponse, 0, len(list))
		for _, s := range list {
			resp = append(resp, toScheduledReportResponse(s))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleCreateScheduledReport(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req scheduledReportRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		input, err := req.toInput()
		if err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		scheduled, err := CreateScheduledReport(r.Context(), pool, firmID, userID, input)
		if err != nil {
			writeScheduledReportsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toScheduledReportResponse(scheduled))
	}
}

func handleUpdateScheduledReport(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		scheduledReportID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid scheduled report id", http.StatusBadRequest)
			return
		}
		var req scheduledReportRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		input, err := req.toInput()
		if err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		scheduled, err := UpdateScheduledReport(r.Context(), pool, firmID, userID, scheduledReportID, input)
		if err != nil {
			writeScheduledReportsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toScheduledReportResponse(scheduled))
	}
}

func handleDeleteScheduledReport(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		scheduledReportID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid scheduled report id", http.StatusBadRequest)
			return
		}
		if err := DeleteScheduledReport(r.Context(), pool, firmID, userID, scheduledReportID); err != nil {
			writeScheduledReportsError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
