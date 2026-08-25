// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package notificationprefs

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// RegisterRoutes wires notificationprefs' HTTP endpoints into mux - same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/notification-preferences", auth(http.HandlerFunc(handleList(pool))))
	mux.Handle("PUT /api/firms/{firmID}/notification-preferences", auth(http.HandlerFunc(handleUpdate(pool))))
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrForbidden):
		http.Error(w, err.Error(), http.StatusForbidden)
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

type preferenceResponse struct {
	NotificationType string `json:"notificationType"`
	Enabled          bool   `json:"enabled"`
}

func toPreferenceResponses(prefs []Preference) []preferenceResponse {
	resp := make([]preferenceResponse, 0, len(prefs))
	for _, p := range prefs {
		resp = append(resp, preferenceResponse{NotificationType: p.NotificationType, Enabled: p.Enabled})
	}
	return resp
}

func handleList(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		prefs, err := List(r.Context(), pool, firmID, userID)
		if err != nil {
			writeError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPreferenceResponses(prefs))
	}
}

// handleUpdate implements PUT .../notification-preferences - the request
// body is the full desired set of {notificationType, enabled} pairs to
// upsert; unmentioned notification types are left untouched.
func handleUpdate(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		var body []preferenceResponse
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		prefs := make([]Preference, 0, len(body))
		for _, p := range body {
			if p.NotificationType == "" {
				http.Error(w, "notificationType is required", http.StatusBadRequest)
				return
			}
			prefs = append(prefs, Preference{NotificationType: p.NotificationType, Enabled: p.Enabled})
		}

		updated, err := Update(r.Context(), pool, firmID, userID, prefs)
		if err != nil {
			writeError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPreferenceResponses(updated))
	}
}
