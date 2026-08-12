// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package absence

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
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

// RegisterRoutes wires absence's HTTP endpoints into mux.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/absences", auth(http.HandlerFunc(handleListAbsences(pool))))
	mux.Handle("POST /api/firms/{firmID}/absences", auth(http.HandlerFunc(handleCreateAbsence(pool))))
	mux.Handle("GET /api/firms/{firmID}/absences/{absenceID}", auth(http.HandlerFunc(handleGetAbsence(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/absences/{absenceID}", auth(http.HandlerFunc(handleUpdateAbsence(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/absences/{absenceID}", auth(http.HandlerFunc(handleDeleteAbsence(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/absences/{absenceID}/approve", auth(http.HandlerFunc(handleApproveAbsence(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/absences/{absenceID}/reject", auth(http.HandlerFunc(handleRejectAbsence(pool))))
}

func writeAbsenceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrAbsenceNotFound), errors.Is(err, ErrNoPendingApproval):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner), errors.Is(err, ErrNotAbsenceOwner), errors.Is(err, workflow.ErrPermissionDenied):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidAbsence), errors.Is(err, ErrAbsenceNotPending):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type absenceResponse struct {
	ID                 string  `json:"id"`
	PersonID           string  `json:"personId"`
	WorkflowInstanceID *string `json:"workflowInstanceId,omitempty"`
	RequestedBy        string  `json:"requestedBy"`
	Type               string  `json:"type"`
	StartDate          string  `json:"startDate"`
	EndDate            string  `json:"endDate"`
	Status             string  `json:"status"`
	ApprovedBy         *string `json:"approvedBy,omitempty"`
	Notes              *string `json:"notes,omitempty"`
	CreatedAt          string  `json:"createdAt"`
}

func toAbsenceResponse(a Absence) absenceResponse {
	var wfID, approvedBy *string
	if a.WorkflowInstanceID != nil {
		s := a.WorkflowInstanceID.String()
		wfID = &s
	}
	if a.ApprovedBy != nil {
		s := a.ApprovedBy.String()
		approvedBy = &s
	}
	return absenceResponse{
		ID: a.ID.String(), PersonID: a.PersonID.String(), WorkflowInstanceID: wfID, RequestedBy: a.RequestedBy.String(),
		Type: string(a.Type), StartDate: a.StartDate.Format(dateLayout), EndDate: a.EndDate.Format(dateLayout),
		Status: string(a.Status), ApprovedBy: approvedBy, Notes: a.Notes, CreatedAt: a.CreatedAt.Format(time.RFC3339),
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

func handleListAbsences(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		absences, err := ListAbsences(r.Context(), pool, firmID, userID)
		if err != nil {
			writeAbsenceError(w, err)
			return
		}
		resp := make([]absenceResponse, 0, len(absences))
		for _, a := range absences {
			resp = append(resp, toAbsenceResponse(a))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createAbsenceRequest struct {
	PersonID  string `json:"personId"`
	Type      string `json:"type"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	Notes     string `json:"notes"`
}

func handleCreateAbsence(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		var req createAbsenceRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		var personID uuid.UUID
		if req.PersonID != "" {
			var err error
			personID, err = uuid.Parse(req.PersonID)
			if err != nil {
				http.Error(w, "invalid personId", http.StatusBadRequest)
				return
			}
		}
		abs, err := CreateAbsence(r.Context(), pool, firmID, userID, CreateAbsenceInput{
			PersonID: personID, Type: Type(req.Type), StartDate: req.StartDate, EndDate: req.EndDate, Notes: req.Notes,
		})
		if err != nil {
			writeAbsenceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toAbsenceResponse(abs))
	}
}

func handleGetAbsence(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		absenceID, err := uuid.Parse(r.PathValue("absenceID"))
		if err != nil {
			http.Error(w, "invalid absence id", http.StatusBadRequest)
			return
		}
		abs, err := GetAbsence(r.Context(), pool, firmID, userID, absenceID)
		if err != nil {
			writeAbsenceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAbsenceResponse(abs))
	}
}

type updateAbsenceRequest struct {
	Type      *string `json:"type"`
	StartDate *string `json:"startDate"`
	EndDate   *string `json:"endDate"`
	Notes     *string `json:"notes"`
}

func handleUpdateAbsence(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		absenceID, err := uuid.Parse(r.PathValue("absenceID"))
		if err != nil {
			http.Error(w, "invalid absence id", http.StatusBadRequest)
			return
		}
		var req updateAbsenceRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		var absType *Type
		if req.Type != nil {
			t := Type(*req.Type)
			absType = &t
		}
		abs, err := UpdateAbsence(r.Context(), pool, firmID, userID, absenceID, UpdateAbsenceInput{
			Type: absType, StartDate: req.StartDate, EndDate: req.EndDate, Notes: req.Notes,
		})
		if err != nil {
			writeAbsenceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAbsenceResponse(abs))
	}
}

func handleDeleteAbsence(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		absenceID, err := uuid.Parse(r.PathValue("absenceID"))
		if err != nil {
			http.Error(w, "invalid absence id", http.StatusBadRequest)
			return
		}
		if err := DeleteAbsence(r.Context(), pool, firmID, userID, absenceID); err != nil {
			writeAbsenceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleApproveAbsence(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		absenceID, err := uuid.Parse(r.PathValue("absenceID"))
		if err != nil {
			http.Error(w, "invalid absence id", http.StatusBadRequest)
			return
		}
		abs, err := Approve(r.Context(), pool, firmID, userID, absenceID)
		if err != nil {
			writeAbsenceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAbsenceResponse(abs))
	}
}

func handleRejectAbsence(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		absenceID, err := uuid.Parse(r.PathValue("absenceID"))
		if err != nil {
			http.Error(w, "invalid absence id", http.StatusBadRequest)
			return
		}
		abs, err := Reject(r.Context(), pool, firmID, userID, absenceID)
		if err != nil {
			writeAbsenceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAbsenceResponse(abs))
	}
}
