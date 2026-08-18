// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// decodeJSONBody mirrors every other package's own copy (e.g.
// internal/edgeagent/handlers.go) - a missing/empty body is a no-op, not
// an error.
func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires this package's HTTP endpoints into mux. Every
// route is owner-gated (see each handler's own doc comment) - configuring
// which AI provider a firm's data gets sent to, and every /ai/* call that
// actually uses that configuration, is a structural firm decision, the
// same tier as internal/edgeagent.CreateAgent.
//
// encryptor may be nil (ZONARYOS_AI_ENCRYPTION_KEY unset) - every handler
// below passes it straight through to the internal/ai function that
// actually needs it, which returns ErrEncryptionKeyNotSet (mapped to 500)
// at the point encryption/decryption is attempted, exactly the
// "installation hasn't set this up yet, don't fail startup" contract
// Encryptor's own doc comment describes.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool, encryptor *Encryptor) {
	auth := identity.Middleware(verifier)

	mux.Handle("POST /api/firms/{firmID}/ai-config", auth(http.HandlerFunc(handleCreateConfig(pool, encryptor))))
	mux.Handle("GET /api/firms/{firmID}/ai-config", auth(http.HandlerFunc(handleListConfigs(pool, encryptor))))
	mux.Handle("DELETE /api/firms/{firmID}/ai-config/{configID}", auth(http.HandlerFunc(handleDeleteConfig(pool))))
	mux.Handle("POST /api/firms/{firmID}/ai/suggest-workflow", auth(http.HandlerFunc(handleSuggestWorkflow(pool, encryptor))))
	mux.Handle("POST /api/firms/{firmID}/ai/suggest-report", auth(http.HandlerFunc(handleSuggestReport(pool, encryptor))))
}

// writeAIError maps this package's sentinel errors to the HTTP status
// that reflects why the caller isn't getting what they asked for - same
// convention as internal/edgeagent's writeEdgeAgentError, with one
// deliberate deviation: ErrNoActiveConfig maps to 404, NOT 403. Part 5's
// own safety-constraint text is explicit that an AI feature must behave
// as if it doesn't exist at all when unconfigured, rather than confirming
// to any firm member that "AI exists but you're not allowed to use it" -
// 403 would leak that distinction, 404 doesn't.
func writeAIError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrConfigNotFound), errors.Is(err, ErrNoActiveConfig):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidConfig):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrInvalidAISuggestion):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	case errors.Is(err, ErrEncryptionKeyNotSet):
		http.Error(w, err.Error(), http.StatusInternalServerError)
	case errors.Is(err, ErrProviderCallFailed), errors.Is(err, ErrProviderNotImplemented):
		http.Error(w, err.Error(), http.StatusBadGateway)
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

// configResponse never includes the plaintext or ciphertext API key - see
// Configuration.APIKeyMasked's own doc comment (crypto.go's MaskAPIKey).
type configResponse struct {
	ID           string         `json:"id"`
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	APIKeyMasked string         `json:"apiKeyMasked"`
	Settings     map[string]any `json:"settings"`
	IsActive     bool           `json:"isActive"`
}

func toConfigResponse(c Configuration) configResponse {
	return configResponse{
		ID: c.ID.String(), Provider: string(c.Provider), Model: c.Model,
		APIKeyMasked: c.APIKeyMasked, Settings: c.Settings, IsActive: c.IsActive,
	}
}

type createConfigRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
}

// handleCreateConfig serves POST .../ai-config (owner-gated) - stores a
// new AI configuration, encrypting APIKey server-side before it ever
// reaches the database. See CreateConfig's own doc comment for the
// single-active-configuration-per-firm enforcement this triggers.
func handleCreateConfig(pool *pgxpool.Pool, encryptor *Encryptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createConfigRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		cfg, err := CreateConfig(r.Context(), pool, encryptor, firmID, userID, CreateConfigInput{
			Provider: ProviderName(req.Provider), Model: req.Model, APIKey: req.APIKey,
		})
		if err != nil {
			writeAIError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toConfigResponse(cfg))
	}
}

// handleListConfigs serves GET .../ai-config (owner-gated) - every
// APIKeyMasked value is computed by decrypting server-side and
// discarding the plaintext immediately, never itself sent to the client.
func handleListConfigs(pool *pgxpool.Pool, encryptor *Encryptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		configs, err := ListConfigs(r.Context(), pool, encryptor, firmID, userID)
		if err != nil {
			writeAIError(w, err)
			return
		}

		resp := make([]configResponse, 0, len(configs))
		for _, c := range configs {
			resp = append(resp, toConfigResponse(c))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// handleDeleteConfig serves DELETE .../ai-config/{configID} (owner-gated).
func handleDeleteConfig(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		configID, err := uuid.Parse(r.PathValue("configID"))
		if err != nil {
			http.Error(w, "invalid config id", http.StatusBadRequest)
			return
		}

		if err := DeleteConfig(r.Context(), pool, firmID, userID, configID); err != nil {
			writeAIError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type suggestWorkflowRequest struct {
	Description string `json:"description"`
}

// handleSuggestWorkflow serves POST .../ai/suggest-workflow (owner-gated).
// See SuggestWorkflow's own doc comment for the "no auto-retry, one
// attempt, then human review" contract - a 422 response's body IS the raw
// AI response text, not a generic error message, so the caller can show
// it to the reviewing owner.
func handleSuggestWorkflow(pool *pgxpool.Pool, encryptor *Encryptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req suggestWorkflowRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		suggestion, err := SuggestWorkflow(r.Context(), pool, encryptor, firmID, userID, req.Description)
		if err != nil {
			writeAIError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(suggestion)
	}
}

type suggestReportRequest struct {
	Question string `json:"question"`
}

// handleSuggestReport serves POST .../ai/suggest-report (owner-gated) -
// same contract as handleSuggestWorkflow, for reports.QuerySpec instead.
func handleSuggestReport(pool *pgxpool.Pool, encryptor *Encryptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req suggestReportRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		spec, err := SuggestReport(r.Context(), pool, encryptor, firmID, userID, req.Question)
		if err != nil {
			writeAIError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(spec)
	}
}
