// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package useractivity

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// RegisterRoutes wires the activity widget's one, read-only HTTP endpoint
// into mux - same /api/firms/{firmID}/... convention and bearer-token
// auth middleware as every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)
	mux.Handle("GET /api/firms/{firmID}/activity", auth(http.HandlerFunc(handleList(pool))))
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type entryResponse struct {
	ID         string         `json:"id"`
	UserID     string         `json:"userId"`
	UserEmail  string         `json:"userEmail"`
	UserName   string         `json:"userName"`
	EventType  string         `json:"eventType"`
	EventData  map[string]any `json:"eventData"`
	OccurredAt string         `json:"occurredAt"`
}

// handleList serves GET .../activity - member-gated, paginated
// (?limit=/?offset=, defaulting to the widget's own "last 50 events"
// spec), optionally filtered to ?userId= and/or ?eventType=. The
// filtered-but-unpaged total is reported via the X-Total-Count response
// header, same convention as internal/auditlog/internal/notification's
// own list handlers.
func handleList(pool *pgxpool.Pool) http.HandlerFunc {
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

		opts, err := parseListOptions(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		result, err := List(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeError(w, err)
			return
		}

		resp := make([]entryResponse, 0, len(result.Entries))
		for _, e := range result.Entries {
			resp = append(resp, entryResponse{
				ID:         e.ID.String(),
				UserID:     e.UserID.String(),
				UserEmail:  e.UserEmail,
				UserName:   e.UserName,
				EventType:  e.EventType,
				EventData:  e.EventData,
				OccurredAt: e.OccurredAt.Format(time.RFC3339),
			})
		}

		w.Header().Set("X-Total-Count", strconv.Itoa(result.Total))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// parseListOptions reads ?limit=/?offset=/?userId=/?eventType= off r into
// ListOptions, rejecting a negative/over-cap limit or offset, or an
// unparseable userId, outright rather than silently ignoring it - same
// convention as internal/auditlog's own parseListOptions.
func parseListOptions(r *http.Request) (ListOptions, error) {
	q := r.URL.Query()
	var opts ListOptions

	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 || limit > maxListLimit {
			return ListOptions{}, errors.New("invalid limit")
		}
		opts.Limit = limit
	}
	if raw := q.Get("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return ListOptions{}, errors.New("invalid offset")
		}
		opts.Offset = offset
	}
	if raw := q.Get("userId"); raw != "" {
		userID, err := uuid.Parse(raw)
		if err != nil {
			return ListOptions{}, errors.New("invalid userId")
		}
		opts.UserID = &userID
	}
	opts.EventType = q.Get("eventType")

	return opts, nil
}
