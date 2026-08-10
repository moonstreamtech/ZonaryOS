// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package webhook

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

// RegisterRoutes wires internal/webhook's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/webhooks", auth(http.HandlerFunc(handleListWebhooks(pool))))
	mux.Handle("POST /api/firms/{firmID}/webhooks", auth(http.HandlerFunc(handleCreateWebhook(pool))))
	mux.Handle("GET /api/firms/{firmID}/webhooks/{webhookID}/deliveries", auth(http.HandlerFunc(handleListDeliveries(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/webhooks/{webhookID}", auth(http.HandlerFunc(handleUpdateWebhook(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/webhooks/{webhookID}", auth(http.HandlerFunc(handleDeleteWebhook(pool))))
}

func writeWebhookError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrWebhookNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidWebhook):
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

type webhookResponse struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	URL             string   `json:"url"`
	Events          []string `json:"events"`
	Secret          string   `json:"secret"`
	IsActive        bool     `json:"isActive"`
	LastTriggeredAt *string  `json:"lastTriggeredAt,omitempty"`
	CreatedAt       string   `json:"createdAt"`
}

func toWebhookResponse(w Webhook) webhookResponse {
	var lastTriggered *string
	if w.LastTriggeredAt != nil {
		s := w.LastTriggeredAt.Format(time.RFC3339)
		lastTriggered = &s
	}
	return webhookResponse{
		ID: w.ID.String(), Name: w.Name, URL: w.URL, Events: w.Events, Secret: w.Secret,
		IsActive: w.IsActive, LastTriggeredAt: lastTriggered, CreatedAt: w.CreatedAt.Format(time.RFC3339),
	}
}

type webhookRequest struct {
	Name     string   `json:"name"`
	URL      string   `json:"url"`
	Events   []string `json:"events"`
	IsActive bool     `json:"isActive"`
}

func (req webhookRequest) toInput() WebhookInput {
	return WebhookInput{Name: req.Name, URL: req.URL, Events: req.Events, IsActive: req.IsActive}
}

func handleListWebhooks(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		hooks, err := ListWebhooks(r.Context(), pool, firmID, userID)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		resp := make([]webhookResponse, 0, len(hooks))
		for _, h := range hooks {
			resp = append(resp, toWebhookResponse(h))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp) // #nosec G117 -- webhookResponse.Secret is deliberately returned here, see webhook.go's own doc comment on why (unlike an API key, a webhook secret isn't a one-time reveal)
	}
}

func handleCreateWebhook(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req webhookRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		hook, err := CreateWebhook(r.Context(), pool, firmID, userID, req.toInput())
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toWebhookResponse(hook)) // #nosec G117 -- see the sibling list handler's own suppression comment
	}
}

func handleUpdateWebhook(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		webhookID, err := uuid.Parse(r.PathValue("webhookID"))
		if err != nil {
			http.Error(w, "invalid webhook id", http.StatusBadRequest)
			return
		}
		var req webhookRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		hook, err := UpdateWebhook(r.Context(), pool, firmID, userID, webhookID, req.toInput())
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toWebhookResponse(hook)) // #nosec G117 -- see handleListWebhooks' own suppression comment
	}
}

func handleDeleteWebhook(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		webhookID, err := uuid.Parse(r.PathValue("webhookID"))
		if err != nil {
			http.Error(w, "invalid webhook id", http.StatusBadRequest)
			return
		}
		if err := DeleteWebhook(r.Context(), pool, firmID, userID, webhookID); err != nil {
			writeWebhookError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type deliveryResponse struct {
	ID             string         `json:"id"`
	EventType      string         `json:"eventType"`
	Payload        map[string]any `json:"payload"`
	ResponseStatus *int           `json:"responseStatus,omitempty"`
	ResponseBody   *string        `json:"responseBody,omitempty"`
	DeliveredAt    string         `json:"deliveredAt"`
	Success        bool           `json:"success"`
}

func handleListDeliveries(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		webhookID, err := uuid.Parse(r.PathValue("webhookID"))
		if err != nil {
			http.Error(w, "invalid webhook id", http.StatusBadRequest)
			return
		}
		deliveries, err := ListDeliveries(r.Context(), pool, firmID, userID, webhookID)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		resp := make([]deliveryResponse, 0, len(deliveries))
		for _, d := range deliveries {
			resp = append(resp, deliveryResponse{
				ID: d.ID.String(), EventType: d.EventType, Payload: d.Payload,
				ResponseStatus: d.ResponseStatus, ResponseBody: d.ResponseBody,
				DeliveredAt: d.DeliveredAt.Format(time.RFC3339), Success: d.Success,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
