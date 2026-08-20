// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apikey

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

// RegisterRoutes wires internal/apikey's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group. Every route here is itself only
// ever reachable via ordinary Keycloak auth in practice (an API key has
// no scope that would let it manage other API keys - CreateAPIKey's own
// scope-validation rules that out, since managing keys isn't a
// permission_key-gated action at all, it's owner-gated directly), but
// nothing here special-cases that; it's a structural consequence of the
// scope design, not a separate check.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/api-keys", auth(http.HandlerFunc(handleListAPIKeys(pool))))
	mux.Handle("POST /api/firms/{firmID}/api-keys", auth(http.HandlerFunc(handleCreateAPIKey(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/api-keys/{keyID}", auth(http.HandlerFunc(handleRevokeAPIKey(pool))))
	// Secrets rotation batch, Part 4: rotate a key in place rather than
	// forcing a delete+recreate cycle (which would also lose the key's
	// own id/scopes/created_by for anything referencing it elsewhere).
	mux.Handle("POST /api/firms/{firmID}/api-keys/{keyID}/rotate", auth(http.HandlerFunc(handleRotateAPIKey(pool))))
}

func writeAPIKeyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrKeyNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidAPIKey), errors.Is(err, ErrScopeExceedsPermissions):
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

type apiKeyResponse struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	CreatedBy  string   `json:"createdBy"`
	LastUsedAt *string  `json:"lastUsedAt,omitempty"`
	ExpiresAt  *string  `json:"expiresAt,omitempty"`
	IsActive   bool     `json:"isActive"`
	CreatedAt  string   `json:"createdAt"`
	Key        *string  `json:"key,omitempty"` // only ever set once, by handleCreateAPIKey
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

func toAPIKeyResponse(k APIKey) apiKeyResponse {
	return apiKeyResponse{
		ID: k.ID.String(), Name: k.Name, Scopes: k.Scopes, CreatedBy: k.CreatedBy.String(),
		LastUsedAt: formatTimePtr(k.LastUsedAt), ExpiresAt: formatTimePtr(k.ExpiresAt),
		IsActive: k.IsActive, CreatedAt: k.CreatedAt.Format(time.RFC3339),
	}
}

func handleListAPIKeys(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		keys, err := ListAPIKeys(r.Context(), pool, firmID, userID)
		if err != nil {
			writeAPIKeyError(w, err)
			return
		}
		resp := make([]apiKeyResponse, 0, len(keys))
		for _, k := range keys {
			resp = append(resp, toAPIKeyResponse(k))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createAPIKeyRequest struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

func handleCreateAPIKey(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createAPIKeyRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		plaintext, key, err := CreateAPIKey(r.Context(), pool, firmID, userID, CreateAPIKeyInput{
			Name: req.Name, Scopes: req.Scopes, ExpiresAt: req.ExpiresAt,
		})
		if err != nil {
			writeAPIKeyError(w, err)
			return
		}

		resp := toAPIKeyResponse(key)
		resp.Key = &plaintext
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleRevokeAPIKey(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		keyID, err := uuid.Parse(r.PathValue("keyID"))
		if err != nil {
			http.Error(w, "invalid API key id", http.StatusBadRequest)
			return
		}
		if err := RevokeAPIKey(r.Context(), pool, firmID, userID, keyID); err != nil {
			writeAPIKeyError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleRotateAPIKey is Part 4's own "rotation without a delete+recreate
// cycle" endpoint - see RotateAPIKey's own doc comment. Response shape
// matches handleCreateAPIKey's (the one-time plaintext under "key"),
// since this is functionally the same "here is a new secret, exactly
// once" contract, just against an existing key's own id.
func handleRotateAPIKey(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		keyID, err := uuid.Parse(r.PathValue("keyID"))
		if err != nil {
			http.Error(w, "invalid API key id", http.StatusBadRequest)
			return
		}
		plaintext, key, err := RotateAPIKey(r.Context(), pool, firmID, userID, keyID)
		if err != nil {
			writeAPIKeyError(w, err)
			return
		}

		resp := toAPIKeyResponse(key)
		resp.Key = &plaintext
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
