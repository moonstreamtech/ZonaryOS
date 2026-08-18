// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package plugin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
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

// RegisterRoutes wires this package's HTTP endpoints into mux. The
// plugins catalog is platform-wide reference data - unauthenticated
// reads (same posture as internal/currency/internal/localization's own
// reference-data GETs), platform-admin-gated writes (same allowlist).
// firm-plugin-configs are the ordinary Keycloak bearer-token, owner-
// gated chain every other firm-scoped route group in this codebase uses.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool, allow platformadmin.Allowlist) {
	auth := identity.Middleware(verifier)

	mux.HandleFunc("GET /api/plugins", handleListPlugins(pool))
	mux.Handle("POST /api/plugins", auth(http.HandlerFunc(handleCreatePlugin(pool, allow))))
	mux.Handle("PATCH /api/plugins/{pluginID}", auth(http.HandlerFunc(handleUpdatePlugin(pool, allow))))
	mux.Handle("DELETE /api/plugins/{pluginID}", auth(http.HandlerFunc(handleDeletePlugin(pool, allow))))

	mux.Handle("GET /api/firms/{firmID}/plugin-configs", auth(http.HandlerFunc(handleListFirmPluginConfigs(pool))))
	mux.Handle("POST /api/firms/{firmID}/plugin-configs", auth(http.HandlerFunc(handleUpsertFirmPluginConfig(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/plugin-configs/{pluginID}", auth(http.HandlerFunc(handleDeleteFirmPluginConfig(pool))))
}

func writePluginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrPluginNotFound), errors.Is(err, ErrFirmPluginConfigNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidPlugin):
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

type catalogEntryResponse struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Version      string         `json:"version"`
	Description  *string        `json:"description,omitempty"`
	Author       *string        `json:"author,omitempty"`
	HomepageURL  *string        `json:"homepageUrl,omitempty"`
	EntryPoint   string         `json:"entryPoint"`
	Capabilities []string       `json:"capabilities"`
	IsEnabled    bool           `json:"isEnabled"`
	ConfigSchema map[string]any `json:"configSchema"`
	CreatedAt    string         `json:"createdAt"`
}

func toCatalogEntryResponse(e CatalogEntry) catalogEntryResponse {
	return catalogEntryResponse{
		ID: e.ID.String(), Name: e.Name, Version: e.Version, Description: e.Description, Author: e.Author,
		HomepageURL: e.HomepageURL, EntryPoint: e.EntryPoint, Capabilities: e.Capabilities, IsEnabled: e.IsEnabled,
		ConfigSchema: e.ConfigSchema, CreatedAt: e.CreatedAt.Format(time.RFC3339),
	}
}

type catalogEntryRequest struct {
	Name         string         `json:"name"`
	Version      string         `json:"version"`
	Description  string         `json:"description"`
	Author       string         `json:"author"`
	HomepageURL  string         `json:"homepageUrl"`
	EntryPoint   string         `json:"entryPoint"`
	Capabilities []string       `json:"capabilities"`
	IsEnabled    bool           `json:"isEnabled"`
	ConfigSchema map[string]any `json:"configSchema"`
}

func (req catalogEntryRequest) toInput() CatalogEntryInput {
	return CatalogEntryInput{
		Name: req.Name, Version: req.Version, Description: req.Description, Author: req.Author,
		HomepageURL: req.HomepageURL, EntryPoint: req.EntryPoint, Capabilities: req.Capabilities,
		IsEnabled: req.IsEnabled, ConfigSchema: req.ConfigSchema,
	}
}

func handleListPlugins(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := ListPlugins(r.Context(), pool)
		if err != nil {
			writePluginError(w, err)
			return
		}
		resp := make([]catalogEntryResponse, 0, len(entries))
		for _, e := range entries {
			resp = append(resp, toCatalogEntryResponse(e))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// handleCreatePlugin serves POST /api/plugins - platform-admin-gated,
// checked as the very first thing after reading the already-verified
// identity off the request context, before any database access - same
// "404, not 403, off-allowlist looks like the route doesn't exist"
// posture internal/currency.handleCreateRate establishes.
func handleCreatePlugin(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		if !allow.Contains(id.Email) {
			http.NotFound(w, r)
			return
		}

		var req catalogEntryRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		entry, err := CreatePlugin(r.Context(), pool, req.toInput())
		if err != nil {
			writePluginError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toCatalogEntryResponse(entry))
	}
}

func handleUpdatePlugin(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		if !allow.Contains(id.Email) {
			http.NotFound(w, r)
			return
		}
		pluginID, err := uuid.Parse(r.PathValue("pluginID"))
		if err != nil {
			http.Error(w, "invalid plugin id", http.StatusBadRequest)
			return
		}
		var req catalogEntryRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		entry, err := UpdatePlugin(r.Context(), pool, pluginID, req.toInput())
		if err != nil {
			writePluginError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toCatalogEntryResponse(entry))
	}
}

func handleDeletePlugin(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		if !allow.Contains(id.Email) {
			http.NotFound(w, r)
			return
		}
		pluginID, err := uuid.Parse(r.PathValue("pluginID"))
		if err != nil {
			http.Error(w, "invalid plugin id", http.StatusBadRequest)
			return
		}
		if err := DeletePlugin(r.Context(), pool, pluginID); err != nil {
			writePluginError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type firmConfigResponse struct {
	ID        string         `json:"id"`
	PluginID  string         `json:"pluginId"`
	Config    map[string]any `json:"config"`
	IsEnabled bool           `json:"isEnabled"`
	CreatedAt string         `json:"createdAt"`
}

func toFirmConfigResponse(c FirmConfig) firmConfigResponse {
	return firmConfigResponse{
		ID: c.ID.String(), PluginID: c.PluginID.String(), Config: c.Config,
		IsEnabled: c.IsEnabled, CreatedAt: c.CreatedAt.Format(time.RFC3339),
	}
}

type firmConfigRequest struct {
	PluginID  string         `json:"pluginId"`
	Config    map[string]any `json:"config"`
	IsEnabled bool           `json:"isEnabled"`
}

func handleListFirmPluginConfigs(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		configs, err := ListFirmPluginConfigs(r.Context(), pool, firmID, userID)
		if err != nil {
			writePluginError(w, err)
			return
		}
		resp := make([]firmConfigResponse, 0, len(configs))
		for _, c := range configs {
			resp = append(resp, toFirmConfigResponse(c))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleUpsertFirmPluginConfig(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req firmConfigRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		pluginID, err := uuid.Parse(req.PluginID)
		if err != nil {
			http.Error(w, "invalid plugin id", http.StatusBadRequest)
			return
		}
		cfg, err := UpsertFirmPluginConfig(r.Context(), pool, firmID, userID, FirmConfigInput{
			PluginID: pluginID, Config: req.Config, IsEnabled: req.IsEnabled,
		})
		if err != nil {
			writePluginError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toFirmConfigResponse(cfg))
	}
}

func handleDeleteFirmPluginConfig(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		pluginID, err := uuid.Parse(r.PathValue("pluginID"))
		if err != nil {
			http.Error(w, "invalid plugin id", http.StatusBadRequest)
			return
		}
		if err := DeleteFirmPluginConfig(r.Context(), pool, firmID, userID, pluginID); err != nil {
			writePluginError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
