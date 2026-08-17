// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package edgeagent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// decodeJSONBody decodes r's body into v - same contract as every other
// package's own copy (a missing/empty body is a no-op, not an error).
func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires edgeagent's HTTP endpoints into mux. Two auth
// chains are registered side by side (see this package's doc comment):
// the ordinary Keycloak bearer-token chain for firm members/owners acting
// through the browser, and bearerAgentToken - this package's own
// token-authenticated chain - for the agent process itself on
// /api/edge/*.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("POST /api/firms/{firmID}/edge-agents", auth(http.HandlerFunc(handleCreateAgent(pool))))
	mux.Handle("GET /api/firms/{firmID}/edge-agents", auth(http.HandlerFunc(handleListAgents(pool))))
	mux.Handle("GET /api/firms/{firmID}/edge-agents/{agentID}", auth(http.HandlerFunc(handleGetAgent(pool))))
	mux.Handle("POST /api/firms/{firmID}/edge-agents/{agentID}/tokens", auth(http.HandlerFunc(handleIssueToken(pool))))
	mux.Handle("GET /api/firms/{firmID}/edge-agents/{agentID}/tokens", auth(http.HandlerFunc(handleListTokens(pool))))
	mux.Handle("GET /api/firms/{firmID}/edge-agents/{agentID}/events", auth(http.HandlerFunc(handleListEvents(pool))))
	mux.Handle("POST /api/firms/{firmID}/edge-agents/{agentID}/commands", auth(http.HandlerFunc(handleCreateCommand(pool))))
	mux.Handle("GET /api/firms/{firmID}/edge-agents/{agentID}/commands", auth(http.HandlerFunc(handleListCommands(pool))))

	// bearerAgentToken (below) is the token path: no Keycloak, no
	// firmID/agentID in the URL at all - the agent's identity comes
	// entirely from the bearer token itself (see AuthenticatedAgent's
	// doc comment).
	mux.Handle("POST /api/edge/heartbeat", http.HandlerFunc(handleHeartbeat(pool)))
	mux.Handle("POST /api/edge/events", http.HandlerFunc(handleRecordEvent(pool)))
	mux.Handle("GET /api/edge/commands", http.HandlerFunc(handlePollCommands(pool)))
	mux.Handle("POST /api/edge/commands/{commandID}/ack", http.HandlerFunc(handleAckCommand(pool)))
}

// writeEdgeAgentError maps this package's sentinel errors to the HTTP
// status that reflects why the caller isn't getting what they asked for -
// same convention as internal/logistics's writeLogisticsError.
func writeEdgeAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrAgentNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrCommandNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrInvalidAgent), errors.Is(err, ErrInvalidCommand):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// bearerToken extracts the raw token from r's Authorization header - same
// "Bearer <token>" convention internal/identity.Middleware uses, but
// checked against edge_agent_tokens rather than Keycloak.
func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	if token == "" {
		return "", false
	}
	return token, true
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

type agentResponse struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Description  *string        `json:"description,omitempty"`
	Status       string         `json:"status"`
	LastSeenAt   *string        `json:"lastSeenAt,omitempty"`
	Version      *string        `json:"version,omitempty"`
	Capabilities []string       `json:"capabilities"`
	Config       map[string]any `json:"config"`
	CreatedAt    string         `json:"createdAt"`
}

func formatTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

func toAgentResponse(a Agent) agentResponse {
	return agentResponse{
		ID: a.ID.String(), Name: a.Name, Description: a.Description, Status: string(a.Status),
		LastSeenAt: formatTime(a.LastSeenAt), Version: a.Version, Capabilities: a.Capabilities,
		Config: a.Config, CreatedAt: a.CreatedAt.Format(time.RFC3339),
	}
}

type createAgentRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func handleCreateAgent(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createAgentRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		agent, err := CreateAgent(r.Context(), pool, firmID, userID, CreateAgentInput{Name: req.Name, Description: req.Description})
		if err != nil {
			writeEdgeAgentError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toAgentResponse(agent))
	}
}

func handleListAgents(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		agents, err := ListAgents(r.Context(), pool, firmID, userID)
		if err != nil {
			writeEdgeAgentError(w, err)
			return
		}

		resp := make([]agentResponse, 0, len(agents))
		for _, a := range agents {
			resp = append(resp, toAgentResponse(a))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleGetAgent(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		agentID, err := uuid.Parse(r.PathValue("agentID"))
		if err != nil {
			http.Error(w, "invalid agent id", http.StatusBadRequest)
			return
		}

		agent, err := GetAgent(r.Context(), pool, firmID, userID, agentID)
		if err != nil {
			writeEdgeAgentError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAgentResponse(agent))
	}
}

type tokenSummaryResponse struct {
	ID         string  `json:"id"`
	AgentID    string  `json:"agentId"`
	Name       string  `json:"name"`
	LastUsedAt *string `json:"lastUsedAt,omitempty"`
	ExpiresAt  *string `json:"expiresAt,omitempty"`
	CreatedAt  string  `json:"createdAt"`
}

func toTokenSummaryResponse(t TokenSummary) tokenSummaryResponse {
	return tokenSummaryResponse{
		ID: t.ID.String(), AgentID: t.AgentID.String(), Name: t.Name,
		LastUsedAt: formatTime(t.LastUsedAt), ExpiresAt: formatTime(t.ExpiresAt), CreatedAt: t.CreatedAt.Format(time.RFC3339),
	}
}

type issueTokenRequest struct {
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

type issueTokenResponse struct {
	Token   string               `json:"token"`
	Summary tokenSummaryResponse `json:"summary"`
}

func handleIssueToken(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		agentID, err := uuid.Parse(r.PathValue("agentID"))
		if err != nil {
			http.Error(w, "invalid agent id", http.StatusBadRequest)
			return
		}
		var req issueTokenRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		plaintext, summary, err := IssueToken(r.Context(), pool, firmID, userID, agentID, IssueTokenInput{Name: req.Name, ExpiresAt: req.ExpiresAt})
		if err != nil {
			writeEdgeAgentError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(issueTokenResponse{Token: plaintext, Summary: toTokenSummaryResponse(summary)})
	}
}

func handleListTokens(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		agentID, err := uuid.Parse(r.PathValue("agentID"))
		if err != nil {
			http.Error(w, "invalid agent id", http.StatusBadRequest)
			return
		}

		tokens, err := ListTokens(r.Context(), pool, firmID, userID, agentID)
		if err != nil {
			writeEdgeAgentError(w, err)
			return
		}

		resp := make([]tokenSummaryResponse, 0, len(tokens))
		for _, t := range tokens {
			resp = append(resp, toTokenSummaryResponse(t))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type eventResponse struct {
	ID         string         `json:"id"`
	AgentID    string         `json:"agentId"`
	EventType  string         `json:"eventType"`
	Payload    map[string]any `json:"payload"`
	ReceivedAt string         `json:"receivedAt"`
}

func toEventResponse(e Event) eventResponse {
	return eventResponse{
		ID: e.ID.String(), AgentID: e.AgentID.String(), EventType: e.EventType,
		Payload: e.Payload, ReceivedAt: e.ReceivedAt.Format(time.RFC3339),
	}
}

func handleListEvents(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		agentID, err := uuid.Parse(r.PathValue("agentID"))
		if err != nil {
			http.Error(w, "invalid agent id", http.StatusBadRequest)
			return
		}

		events, err := ListEvents(r.Context(), pool, firmID, userID, agentID)
		if err != nil {
			writeEdgeAgentError(w, err)
			return
		}

		resp := make([]eventResponse, 0, len(events))
		for _, e := range events {
			resp = append(resp, toEventResponse(e))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type heartbeatRequest struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func handleHeartbeat(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		var req heartbeatRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		agent, err := Heartbeat(r.Context(), pool, token, HeartbeatInput{Status: AgentStatus(req.Status), Version: req.Version})
		if err != nil {
			if errors.Is(err, ErrInvalidToken) {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			writeEdgeAgentError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAgentResponse(agent))
	}
}

type recordEventRequest struct {
	EventType string         `json:"eventType"`
	Payload   map[string]any `json:"payload"`
}

func handleRecordEvent(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		var req recordEventRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		event, err := RecordEvent(r.Context(), pool, token, req.EventType, req.Payload)
		if err != nil {
			if errors.Is(err, ErrInvalidToken) {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			writeEdgeAgentError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toEventResponse(event))
	}
}

type commandResponse struct {
	ID             string         `json:"id"`
	AgentID        string         `json:"agentId"`
	CommandType    string         `json:"commandType"`
	Payload        map[string]any `json:"payload"`
	Status         string         `json:"status"`
	CreatedAt      string         `json:"createdAt"`
	DeliveredAt    *string        `json:"deliveredAt,omitempty"`
	AcknowledgedAt *string        `json:"acknowledgedAt,omitempty"`
}

func toCommandResponse(c Command) commandResponse {
	return commandResponse{
		ID: c.ID.String(), AgentID: c.AgentID.String(), CommandType: c.CommandType, Payload: c.Payload,
		Status: string(c.Status), CreatedAt: c.CreatedAt.Format(time.RFC3339),
		DeliveredAt: formatTime(c.DeliveredAt), AcknowledgedAt: formatTime(c.AcknowledgedAt),
	}
}

type createCommandRequest struct {
	CommandType string         `json:"commandType"`
	Payload     map[string]any `json:"payload"`
}

// handleCreateCommand serves POST .../edge-agents/{agentID}/commands
// (owner-gated) - Part 2's "server sends a command to an agent" half.
func handleCreateCommand(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		agentID, err := uuid.Parse(r.PathValue("agentID"))
		if err != nil {
			http.Error(w, "invalid agent id", http.StatusBadRequest)
			return
		}
		var req createCommandRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		command, err := CreateCommand(r.Context(), pool, firmID, userID, agentID, CreateCommandInput{CommandType: req.CommandType, Payload: req.Payload})
		if err != nil {
			writeEdgeAgentError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toCommandResponse(command))
	}
}

// handleListCommands serves GET .../edge-agents/{agentID}/commands
// (member-gated) - the edge agent detail page's command history.
func handleListCommands(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		agentID, err := uuid.Parse(r.PathValue("agentID"))
		if err != nil {
			http.Error(w, "invalid agent id", http.StatusBadRequest)
			return
		}

		commands, err := ListCommands(r.Context(), pool, firmID, userID, agentID)
		if err != nil {
			writeEdgeAgentError(w, err)
			return
		}

		resp := make([]commandResponse, 0, len(commands))
		for _, c := range commands {
			resp = append(resp, toCommandResponse(c))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// handlePollCommands serves GET /api/edge/commands (token-authenticated)
// - the agent's own poll loop, Part 2's "agent picks up its pending
// commands" half. Same unwrapped /api/edge/... convention as heartbeat/
// events: no Keycloak, no firmID/agentID in the URL, identity comes
// entirely from the bearer token.
func handlePollCommands(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}

		commands, err := PollCommands(r.Context(), pool, token)
		if err != nil {
			if errors.Is(err, ErrInvalidToken) {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			writeEdgeAgentError(w, err)
			return
		}

		resp := make([]commandResponse, 0, len(commands))
		for _, c := range commands {
			resp = append(resp, toCommandResponse(c))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// handleAckCommand serves POST /api/edge/commands/{commandID}/ack
// (token-authenticated) - the agent reporting it has finished executing
// a command.
func handleAckCommand(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		commandID, err := uuid.Parse(r.PathValue("commandID"))
		if err != nil {
			http.Error(w, "invalid command id", http.StatusBadRequest)
			return
		}

		command, err := AckCommand(r.Context(), pool, token, commandID)
		if err != nil {
			if errors.Is(err, ErrInvalidToken) {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			writeEdgeAgentError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toCommandResponse(command))
	}
}
