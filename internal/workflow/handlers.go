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
	mux.Handle("GET /api/firms/{firmID}/workflow-definitions", auth(http.HandlerFunc(handleWorkflowDefinitions(pool))))
	mux.Handle("POST /api/firms/{firmID}/workflow-definitions", auth(http.HandlerFunc(handleDefineWorkflow(pool))))
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
	case errors.Is(err, ErrNoSuchTransition), errors.Is(err, ErrInvalidSpec):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrDefinitionKeyExists):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type definitionInfoResponse struct {
	DefinitionID        string `json:"definitionId"`
	Key                 string `json:"key"`
	Name                string `json:"name"`
	CreatePermissionKey string `json:"createPermissionKey"`
}

func toDefinitionInfoResponse(info DefinitionInfo) definitionInfoResponse {
	return definitionInfoResponse{
		DefinitionID:        info.ID.String(),
		Key:                 info.Key,
		Name:                info.Name,
		CreatePermissionKey: info.CreatePermissionKey,
	}
}

// handleWorkflowDefinitions serves GET /api/firms/{firmID}/workflow-definitions,
// dispatching on whether a ?key= query parameter was given rather than a
// second route: with one, it resolves that well-known key (e.g.
// "stock_to_sale") to the firm-scoped definition ID the rest of this
// package's endpoints take - what lets a frontend page that only knows
// the key avoid a "list everything and find the one I want" round trip.
// Without one, it lists every workflow definition the firm has (see
// ListDefinitions) - the data source for a firm-level "Workflows" view
// that doesn't hardcode one card per known workflow. A second path
// segment (e.g. a literal "all") was considered instead of the query
// parameter, but this path already collides at startup with the sibling
// `{definitionID}/instances` pattern below for any such literal segment
// (see RegisterRoutes's comment) - a query parameter sidesteps that the
// same way `?key=` already did.
func handleWorkflowDefinitions(pool *pgxpool.Pool) http.HandlerFunc {
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

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		key := r.URL.Query().Get("key")
		if key == "" {
			defs, err := ListDefinitions(r.Context(), pool, firmID, userID)
			if err != nil {
				writeEngineError(w, err)
				return
			}
			resp := make([]definitionInfoResponse, 0, len(defs))
			for _, d := range defs {
				resp = append(resp, toDefinitionInfoResponse(d))
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		info, err := LookupDefinitionByKey(r.Context(), pool, firmID, userID, key)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toDefinitionInfoResponse(info))
	}
}

type definePermissionRequest struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

type defineStateRequest struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	IsInitial  bool   `json:"isInitial"`
	IsTerminal bool   `json:"isTerminal"`
}

type defineTransitionRequest struct {
	FromStateKey string                  `json:"fromStateKey"`
	ToStateKey   string                  `json:"toStateKey"`
	ActionKey    string                  `json:"actionKey"`
	Name         string                  `json:"name"`
	Permission   definePermissionRequest `json:"permission"`
}

// defineWorkflowRequest is DefinitionSpec's JSON wire shape (see spec.go)
// - a 1:1 mapping, not a reduced/simplified one, since the whole point of
// this endpoint is to let a firm's owner define exactly the same kind of
// spec SeedStockToSaleWorkflow already builds in Go.
type defineWorkflowRequest struct {
	Key              string                    `json:"key"`
	Name             string                    `json:"name"`
	CreatePermission definePermissionRequest   `json:"createPermission"`
	States           []defineStateRequest      `json:"states"`
	Transitions      []defineTransitionRequest `json:"transitions"`
}

// handleDefineWorkflow lets a firm's owner define a new workflow (item 1
// of this batch: expose internal/workflow.DefineWorkflow, previously only
// reachable from Go fixtures/the firm-creation wizard) - owner-gated via
// DefineWorkflowForFirm, which also handles the self-action auto-grant
// so the caller's own role can actually use what it just defined. Spec
// structural validation (DefinitionSpec.Validate) and a duplicate-key
// collision are both surfaced as real 4xx responses via writeEngineError
// (ErrInvalidSpec -> 400, ErrDefinitionKeyExists -> 409), not swallowed
// into a generic 500.
func handleDefineWorkflow(pool *pgxpool.Pool) http.HandlerFunc {
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

		var req defineWorkflowRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		spec := DefinitionSpec{
			Key:  req.Key,
			Name: req.Name,
			CreatePermission: PermissionSpec{
				Key:         req.CreatePermission.Key,
				Description: req.CreatePermission.Description,
			},
			States:      make([]StateSpec, 0, len(req.States)),
			Transitions: make([]TransitionSpec, 0, len(req.Transitions)),
		}
		for _, s := range req.States {
			spec.States = append(spec.States, StateSpec{
				Key: s.Key, Name: s.Name, IsInitial: s.IsInitial, IsTerminal: s.IsTerminal,
			})
		}
		for _, t := range req.Transitions {
			spec.Transitions = append(spec.Transitions, TransitionSpec{
				FromStateKey: t.FromStateKey,
				ToStateKey:   t.ToStateKey,
				ActionKey:    t.ActionKey,
				Name:         t.Name,
				Permission: PermissionSpec{
					Key:         t.Permission.Key,
					Description: t.Permission.Description,
				},
			})
		}

		definitionID, err := DefineWorkflowForFirm(r.Context(), pool, firmID, userID, spec)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toDefinitionInfoResponse(DefinitionInfo{
			ID:                  definitionID,
			Key:                 spec.Key,
			Name:                spec.Name,
			CreatePermissionKey: spec.CreatePermission.Key,
		}))
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
