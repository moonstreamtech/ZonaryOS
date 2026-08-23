// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package onboarding

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// RegisterRoutes wires onboarding's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/onboarding", auth(http.HandlerFunc(handleGetProgress(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/onboarding", auth(http.HandlerFunc(handleDismiss(pool))))
}

func writeOnboardingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type progressResponse struct {
	CompletedSteps []string `json:"completedSteps"`
	Steps          []string `json:"steps"`
	DismissedAt    *string  `json:"dismissedAt,omitempty"`
}

func toProgressResponse(p Progress) progressResponse {
	completed := make([]string, 0, len(p.CompletedSteps))
	for _, s := range p.CompletedSteps {
		completed = append(completed, string(s))
	}
	steps := make([]string, 0, len(Steps))
	for _, s := range Steps {
		steps = append(steps, string(s))
	}
	return progressResponse{CompletedSteps: completed, Steps: steps, DismissedAt: p.DismissedAt}
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

func handleGetProgress(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		progress, err := GetProgress(r.Context(), pool, firmID, userID)
		if err != nil {
			writeOnboardingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toProgressResponse(progress))
	}
}

// handleDismiss implements PATCH /api/firms/{firmID}/onboarding - its
// only supported mutation is dismissal (the design brief's "get
// progress, mark dismissed"; step completion is automatic, never via
// this endpoint - see CompleteStep's own doc comment).
func handleDismiss(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		progress, err := Dismiss(r.Context(), pool, firmID, userID)
		if err != nil {
			writeOnboardingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toProgressResponse(progress))
	}
}
