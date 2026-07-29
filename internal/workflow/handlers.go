// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// decodeJSONBody decodes r's body into v. A missing/empty body is treated
// as "no payload given" (v is left at its zero value), not an error -
// callers like "add stock" with nothing but defaults should still work.
func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires the workflow engine's HTTP endpoints into mux,
// protected by verifier's bearer-token check (same auth middleware
// internal/identity's own routes use). Firm-scoped, mirroring
// internal/identity's /api/me/firms/{firmID}/... path convention.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	// GET .../workflow-definitions (with a required ?key= query param) and
	// GET .../workflow-definitions/{definitionID}/instances briefly shared
	// a path shape ("by-key/{key}" vs "{definitionID}/instances") that
	// net/http's ServeMux rejected as ambiguous at startup - neither
	// pattern is more specific than the other at every differing segment.
	// A query parameter on the collection path sidesteps that: it's a
	// strictly shorter path than the instances-listing one, and the
	// mux never has to compare wildcard-vs-literal at the same segment.
	mux.Handle("GET /api/firms/{firmID}/workflow-definitions", auth(http.HandlerFunc(handleLookupDefinitionByKey(pool))))
	mux.Handle("POST /api/firms/{firmID}/workflow-definitions/{definitionID}/instances", auth(http.HandlerFunc(handleCreateInstance(pool))))
	mux.Handle("GET /api/firms/{firmID}/workflow-definitions/{definitionID}/instances", auth(http.HandlerFunc(handleListInstances(pool))))
	mux.Handle("GET /api/firms/{firmID}/workflow-instances/{instanceID}", auth(http.HandlerFunc(handleCurrentState(pool))))
	mux.Handle("POST /api/firms/{firmID}/workflow-instances/{instanceID}/transitions/{actionKey}", auth(http.HandlerFunc(handleExecuteTransition(pool))))
}

type stateInfoResponse struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type availableActionResponse struct {
	ActionKey     string            `json:"actionKey"`
	Name          string            `json:"name"`
	ToState       stateInfoResponse `json:"toState"`
	PermissionKey string            `json:"permissionKey"`
}

type instanceStateResponse struct {
	InstanceID           string                    `json:"instanceId"`
	WorkflowDefinitionID string                    `json:"workflowDefinitionId"`
	State                stateInfoResponse         `json:"state"`
	Payload              map[string]any            `json:"payload"`
	AvailableActions     []availableActionResponse `json:"availableActions"`
}

func toInstanceStateResponse(s InstanceState) instanceStateResponse {
	resp := instanceStateResponse{
		InstanceID:           s.InstanceID.String(),
		WorkflowDefinitionID: s.WorkflowDefinitionID.String(),
		State:                stateInfoResponse{Key: s.State.Key, Name: s.State.Name},
		Payload:              s.Payload,
		AvailableActions:     make([]availableActionResponse, 0, len(s.AvailableActions)),
	}
	for _, a := range s.AvailableActions {
		resp.AvailableActions = append(resp.AvailableActions, availableActionResponse{
			ActionKey:     a.ActionKey,
			Name:          a.Name,
			ToState:       stateInfoResponse{Key: a.ToState.Key, Name: a.ToState.Name},
			PermissionKey: a.PermissionKey,
		})
	}
	return resp
}

// writeEngineError maps this package's sentinel errors to the HTTP status
// that reflects why the caller is not getting what they asked for: 404 for
// something not visible in their firm context (RLS or a genuinely wrong
// ID look the same from here, by design), 403 for a real permission
// denial, 400 for a structurally invalid request.
func writeEngineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrDefinitionNotFound), errors.Is(err, ErrInstanceNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrPermissionDenied):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrNoSuchTransition):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type definitionInfoResponse struct {
	DefinitionID string `json:"definitionId"`
	Key          string `json:"key"`
	Name         string `json:"name"`
}

// handleLookupDefinitionByKey resolves a well-known workflow key (e.g.
// "stock_to_sale", given as ?key=stock_to_sale) to the firm-scoped
// definition ID the rest of this package's endpoints take - what lets a
// frontend page that only knows the key avoid a separate "list all
// definitions and find the one I want" round trip.
func handleLookupDefinitionByKey(pool *pgxpool.Pool) http.HandlerFunc {
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

		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "missing key query parameter", http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		info, err := LookupDefinitionByKey(r.Context(), pool, firmID, userID, key)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(definitionInfoResponse{
			DefinitionID: info.ID.String(),
			Key:          info.Key,
			Name:         info.Name,
		})
	}
}

// handleListInstances lists every instance of one workflow definition
// within the caller's firm (e.g. a stock list) - not filtered by the
// caller's own *permissions* (same convention as handleCurrentState:
// enforcement happens when an action is actually executed, not when
// instances are merely listed), but it is filtered by *membership* -
// see ListInstances's doc comment.
func handleListInstances(pool *pgxpool.Pool) http.HandlerFunc {
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
		definitionID, err := uuid.Parse(r.PathValue("definitionID"))
		if err != nil {
			http.Error(w, "invalid workflow definition id", http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		instances, err := ListInstances(r.Context(), pool, firmID, userID, definitionID)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		resp := make([]instanceStateResponse, 0, len(instances))
		for _, inst := range instances {
			resp = append(resp, toInstanceStateResponse(inst))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createInstanceRequest struct {
	Payload map[string]any `json:"payload"`
}

func handleCreateInstance(pool *pgxpool.Pool) http.HandlerFunc {
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
		definitionID, err := uuid.Parse(r.PathValue("definitionID"))
		if err != nil {
			http.Error(w, "invalid workflow definition id", http.StatusBadRequest)
			return
		}

		var req createInstanceRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		instanceID, err := CreateInstance(r.Context(), pool, firmID, userID, definitionID, req.Payload)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		state, err := CurrentState(r.Context(), pool, firmID, userID, instanceID)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toInstanceStateResponse(state))
	}
}

func handleCurrentState(pool *pgxpool.Pool) http.HandlerFunc {
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
		instanceID, err := uuid.Parse(r.PathValue("instanceID"))
		if err != nil {
			http.Error(w, "invalid workflow instance id", http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		state, err := CurrentState(r.Context(), pool, firmID, userID, instanceID)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toInstanceStateResponse(state))
	}
}

type executeTransitionRequest struct {
	Payload map[string]any `json:"payload"`
}

func handleExecuteTransition(pool *pgxpool.Pool) http.HandlerFunc {
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
		instanceID, err := uuid.Parse(r.PathValue("instanceID"))
		if err != nil {
			http.Error(w, "invalid workflow instance id", http.StatusBadRequest)
			return
		}
		actionKey := r.PathValue("actionKey")

		var req executeTransitionRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		if err := ExecuteTransition(r.Context(), pool, firmID, userID, instanceID, actionKey, req.Payload); err != nil {
			writeEngineError(w, err)
			return
		}

		state, err := CurrentState(r.Context(), pool, firmID, userID, instanceID)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toInstanceStateResponse(state))
	}
}
