// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package timetracking

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

// RegisterRoutes wires time tracking's HTTP endpoints into mux.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/time-entries", auth(http.HandlerFunc(handleListEntries(pool))))
	mux.Handle("POST /api/firms/{firmID}/time-entries", auth(http.HandlerFunc(handleCreateEntry(pool))))
	mux.Handle("GET /api/firms/{firmID}/time-entries/summary", auth(http.HandlerFunc(handleSummary(pool))))
	mux.Handle("GET /api/firms/{firmID}/time-entries/{entryID}", auth(http.HandlerFunc(handleGetEntry(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/time-entries/{entryID}", auth(http.HandlerFunc(handleUpdateEntry(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/time-entries/{entryID}", auth(http.HandlerFunc(handleDeleteEntry(pool))))
}

func writeTimeTrackingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrEntryNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotEntryOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidEntry):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type entryResponse struct {
	ID                 string  `json:"id"`
	PersonID           string  `json:"personId"`
	WorkflowInstanceID *string `json:"workflowInstanceId,omitempty"`
	CreatedBy          string  `json:"createdBy"`
	Date               string  `json:"date"`
	Hours              string  `json:"hours"`
	Description        *string `json:"description,omitempty"`
	Billable           bool    `json:"billable"`
	CreatedAt          string  `json:"createdAt"`
}

func toEntryResponse(e Entry) entryResponse {
	var wfID *string
	if e.WorkflowInstanceID != nil {
		s := e.WorkflowInstanceID.String()
		wfID = &s
	}
	return entryResponse{
		ID: e.ID.String(), PersonID: e.PersonID.String(), WorkflowInstanceID: wfID, CreatedBy: e.CreatedBy.String(),
		Date: e.Date.Format(dateLayout), Hours: e.Hours, Description: e.Description, Billable: e.Billable,
		CreatedAt: e.CreatedAt.Format(time.RFC3339),
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

func handleListEntries(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		opts := ListEntriesOptions{From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to")}
		if pid := r.URL.Query().Get("person_id"); pid != "" {
			personID, err := uuid.Parse(pid)
			if err != nil {
				http.Error(w, "invalid person_id", http.StatusBadRequest)
				return
			}
			opts.PersonID = personID
		}
		entries, err := ListEntries(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeTimeTrackingError(w, err)
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
	PersonID    string `json:"personId"`
	Date        string `json:"date"`
	Hours       string `json:"hours"`
	Description string `json:"description"`
	Billable    bool   `json:"billable"`
}

func handleCreateEntry(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		var req entryRequest
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
		entry, err := CreateEntry(r.Context(), pool, firmID, userID, EntryInput{
			PersonID: personID, Date: req.Date, Hours: req.Hours, Description: req.Description, Billable: req.Billable,
		})
		if err != nil {
			writeTimeTrackingError(w, err)
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
			writeTimeTrackingError(w, err)
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
		entry, err := UpdateEntry(r.Context(), pool, firmID, userID, entryID, EntryInput{
			Date: req.Date, Hours: req.Hours, Description: req.Description, Billable: req.Billable,
		})
		if err != nil {
			writeTimeTrackingError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toEntryResponse(entry))
	}
}

func handleDeleteEntry(pool *pgxpool.Pool) http.HandlerFunc {
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
		if err := DeleteEntry(r.Context(), pool, firmID, userID, entryID); err != nil {
			writeTimeTrackingError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type summaryResponse struct {
	PersonID   string `json:"personId"`
	TotalHours string `json:"totalHours"`
}

func handleSummary(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok := resolveCaller(r, pool)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		opts := SummaryOptions{From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to")}
		if pid := r.URL.Query().Get("person_id"); pid != "" {
			personID, err := uuid.Parse(pid)
			if err != nil {
				http.Error(w, "invalid person_id", http.StatusBadRequest)
				return
			}
			opts.PersonID = personID
		}
		totals, err := Summary(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeTimeTrackingError(w, err)
			return
		}
		resp := make([]summaryResponse, 0, len(totals))
		for _, t := range totals {
			resp = append(resp, summaryResponse{PersonID: t.PersonID.String(), TotalHours: t.TotalHours})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
