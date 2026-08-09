// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package notification

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

// RegisterRoutes wires notification's HTTP endpoints into mux - the
// ordinary Keycloak bearer-token chain, same convention as every other
// firm-scoped route group in this codebase.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/notifications", auth(http.HandlerFunc(handleListNotifications(pool))))
	mux.Handle("GET /api/firms/{firmID}/notifications/unread-count", auth(http.HandlerFunc(handleUnreadCount(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/notifications/{id}/read", auth(http.HandlerFunc(handleMarkRead(pool))))
}

func writeNotificationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrNotificationNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
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

func formatTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

type notificationResponse struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	Payload   map[string]any `json:"payload"`
	ReadAt    *string        `json:"readAt,omitempty"`
	CreatedAt string         `json:"createdAt"`
}

func toNotificationResponse(n Notification) notificationResponse {
	return notificationResponse{
		ID: n.ID.String(), Type: n.Type, Title: n.Title, Body: n.Body,
		Payload: n.Payload, ReadAt: formatTime(n.ReadAt), CreatedAt: n.CreatedAt.Format(time.RFC3339),
	}
}

// handleListNotifications serves GET .../notifications - member-gated
// (each caller sees only their own, see ListForUser's own doc comment),
// optional ?unread=true filter, ?limit=/?offset= paging (capped at 200,
// same convention internal/auditlog's own list handler uses). The
// filtered-but-unpaged total is reported via the X-Total-Count response
// header, not a wrapper object, matching that same convention.
func handleListNotifications(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		opts, err := parseListOptions(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		result, err := ListForUser(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeNotificationError(w, err)
			return
		}

		resp := make([]notificationResponse, 0, len(result.Notifications))
		for _, n := range result.Notifications {
			resp = append(resp, toNotificationResponse(n))
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(result.Total))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// parseListOptions reads ?unread=/?limit=/?offset= off r into
// ListOptions - a max limit of 200 mirrors internal/auditlog's own
// paging cap.
func parseListOptions(r *http.Request) (ListOptions, error) {
	q := r.URL.Query()
	var opts ListOptions
	opts.UnreadOnly = q.Get("unread") == "true"
	if v := q.Get("limit"); v != "" {
		limit, err := strconv.Atoi(v)
		if err != nil || limit < 0 || limit > 200 {
			return ListOptions{}, errors.New("invalid limit")
		}
		opts.Limit = limit
	}
	if v := q.Get("offset"); v != "" {
		offset, err := strconv.Atoi(v)
		if err != nil || offset < 0 {
			return ListOptions{}, errors.New("invalid offset")
		}
		opts.Offset = offset
	}
	return opts, nil
}

func handleUnreadCount(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		count, err := UnreadCount(r.Context(), pool, firmID, userID)
		if err != nil {
			writeNotificationError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"count": count})
	}
}

func handleMarkRead(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		notificationID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid notification id", http.StatusBadRequest)
			return
		}

		if err := MarkRead(r.Context(), pool, firmID, userID, notificationID); err != nil {
			writeNotificationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
