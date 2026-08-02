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
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// maxListInstancesLimit caps the ?limit= query param on GET
// .../instances - large enough for any reasonable page size, small
// enough that a caller can't turn "paginated" into "everything" by
// just asking for a huge page.
const maxListInstancesLimit = 200

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
	// A separate top-level path, not nested under /workflow-definitions,
	// deliberately: it aggregates across every definition in the firm at
	// once (see InstanceCountsByDefinition), it isn't scoped to one
	// {definitionID} the way the routes below are. Additive-only (Rule 6)
	// - no existing route's shape or response changes.
	mux.Handle("GET /api/firms/{firmID}/workflow-instance-counts", auth(http.HandlerFunc(handleInstanceCounts(pool))))
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
	case errors.Is(err, ErrNoSuchTransition), errors.Is(err, ErrInvalidSpec), errors.Is(err, ErrPayloadValidation):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrDefinitionKeyExists):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// fieldSpecResponse is FieldSpec's (spec.go) JSON wire shape - included on
// definitionInfoResponse so a frontend resolving a definition (e.g.
// CreateInstanceForm) can render typed fields when a schema is present,
// without a second round trip. Omitted entirely (via omitempty) for a
// schema-less definition, so an existing API consumer that doesn't know
// about item 35 sees no new field at all on stock_to_sale/
// customer_pipeline's responses - purely additive, Never-Violate Rule 6.
type fieldSpecResponse struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	// Options/ReferenceDefinitionKey/ArrayItemType are item 38's additions
	// - see FieldSpec's own doc comment (spec.go) for what each is scoped
	// to. omitempty means a schema field written before this batch (or any
	// non-enum/reference/array field) serializes with none of these three
	// keys present at all, purely additive over item 35's original wire
	// shape.
	Options                []string `json:"options,omitempty"`
	ReferenceDefinitionKey string   `json:"referenceDefinitionKey,omitempty"`
	ArrayItemType          string   `json:"arrayItemType,omitempty"`
}

func toFieldSpecResponses(fields []FieldSpec) []fieldSpecResponse {
	if len(fields) == 0 {
		return nil
	}
	resp := make([]fieldSpecResponse, 0, len(fields))
	for _, f := range fields {
		resp = append(resp, fieldSpecResponse{
			Name:                   f.Name,
			Type:                   string(f.Type),
			Required:               f.Required,
			Options:                f.Options,
			ReferenceDefinitionKey: f.ReferenceDefinitionKey,
			ArrayItemType:          f.ArrayItemType,
		})
	}
	return resp
}

type definitionInfoResponse struct {
	DefinitionID        string              `json:"definitionId"`
	Key                 string              `json:"key"`
	Name                string              `json:"name"`
	CreatePermissionKey string              `json:"createPermissionKey"`
	Fields              []fieldSpecResponse `json:"fields,omitempty"`
}

func toDefinitionInfoResponse(info DefinitionInfo) definitionInfoResponse {
	return definitionInfoResponse{
		DefinitionID:        info.ID.String(),
		Key:                 info.Key,
		Name:                info.Name,
		CreatePermissionKey: info.CreatePermissionKey,
		Fields:              toFieldSpecResponses(info.Fields),
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

type stateCountResponse struct {
	StateKey  string `json:"stateKey"`
	StateName string `json:"stateName"`
	Count     int    `json:"count"`
}

type definitionInstanceCountsResponse struct {
	DefinitionID string               `json:"definitionId"`
	Key          string               `json:"key"`
	Name         string               `json:"name"`
	Counts       []stateCountResponse `json:"counts"`
}

func toDefinitionInstanceCountsResponse(d DefinitionInstanceCounts) definitionInstanceCountsResponse {
	resp := definitionInstanceCountsResponse{
		DefinitionID: d.DefinitionID.String(),
		Key:          d.Key,
		Name:         d.Name,
		Counts:       make([]stateCountResponse, 0, len(d.Counts)),
	}
	for _, c := range d.Counts {
		resp.Counts = append(resp.Counts, stateCountResponse{
			StateKey:  c.StateKey,
			StateName: c.StateName,
			Count:     c.Count,
		})
	}
	return resp
}

// handleInstanceCounts serves GET /api/firms/{firmID}/workflow-instance-counts
// (item 2 of this batch): the dashboard's overview data, every workflow
// definition the firm has, each with its instance count broken down by
// state - see InstanceCountsByDefinition. Not filtered by the caller's
// own permissions, same convention as handleWorkflowDefinitions/
// handleListInstances (enforcement happens at action time, not at read
// time); it is filtered by membership.
func handleInstanceCounts(pool *pgxpool.Pool) http.HandlerFunc {
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

		counts, err := InstanceCountsByDefinition(r.Context(), pool, firmID, userID)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		resp := make([]definitionInstanceCountsResponse, 0, len(counts))
		for _, c := range counts {
			resp = append(resp, toDefinitionInstanceCountsResponse(c))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
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

// defineFieldRequest is FieldSpec's (spec.go) JSON wire shape for the
// request side - mirrors fieldSpecResponse's shape (this file's response
// side) so the wire format round-trips, but stays a separate type the
// same way defineStateRequest/StateSpec stay separate from
// stateInfoResponse/StateInfo elsewhere in this file. Omitting `fields`
// entirely from the request body (or sending an empty array) is exactly
// DefinitionSpec.Fields' own "nil/empty means no schema" contract -
// nothing about this field is required for handleDefineWorkflow's caller
// to keep working exactly as before item 35.
type defineFieldRequest struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	// Options/ReferenceDefinitionKey/ArrayItemType are item 38's additions
	// - see fieldSpecResponse's own doc comment above for the same
	// additive/omitempty reasoning on the response side.
	Options                []string `json:"options,omitempty"`
	ReferenceDefinitionKey string   `json:"referenceDefinitionKey,omitempty"`
	ArrayItemType          string   `json:"arrayItemType,omitempty"`
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
	// Fields is OPTIONAL (Open Points item 35) - an owner defining a new
	// workflow via DefinitionBuilder.tsx who never adds a field here gets
	// exactly today's freeform-payload workflow, unchanged.
	Fields []defineFieldRequest `json:"fields"`
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
		if len(req.Fields) > 0 {
			spec.Fields = make([]FieldSpec, 0, len(req.Fields))
			for _, f := range req.Fields {
				spec.Fields = append(spec.Fields, FieldSpec{
					Name:                   f.Name,
					Type:                   FieldType(f.Type),
					Required:               f.Required,
					Options:                f.Options,
					ReferenceDefinitionKey: f.ReferenceDefinitionKey,
					ArrayItemType:          f.ArrayItemType,
				})
			}
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
			Fields:              spec.Fields,
		}))
	}
}

// handleListInstances lists instances of one workflow definition within
// the caller's firm (e.g. a stock list) - not filtered by the caller's
// own *permissions* (same convention as handleCurrentState: enforcement
// happens when an action is actually executed, not when instances are
// merely listed), but it is filtered by *membership* - see
// ListInstances's doc comment.
//
// Optional query params: ?limit= and ?offset= for paging (both default
// to "off" - a request with neither returns every instance, unpaged,
// same response shape as before this endpoint supported paging - see
// ListInstancesOptions's zero value), and ?q= for a generic text filter
// (matched against instance state and payload - see ListInstances). The
// response body stays a plain array either way, so no existing caller
// (WorkflowHistory's own correlation fetch, any external API consumer)
// breaks; the total matching count (before paging) is reported via the
// X-Total-Count response header instead of changing the body shape -
// Never-Violate Rule 6, no breaking change without a deprecation cycle.
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

		opts, err := parseListInstancesOptions(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		result, err := ListInstances(r.Context(), pool, firmID, userID, definitionID, opts)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		resp := make([]instanceStateResponse, 0, len(result.Instances))
		for _, inst := range result.Instances {
			resp = append(resp, toInstanceStateResponse(inst))
		}

		w.Header().Set("X-Total-Count", strconv.Itoa(result.Total))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// parseListInstancesOptions reads ?limit=/?offset=/?q= off r into
// ListInstancesOptions, rejecting a negative or over-cap limit/offset
// outright rather than silently clamping - a caller sending garbage
// should see a 400, not a quietly "corrected" page.
func parseListInstancesOptions(r *http.Request) (ListInstancesOptions, error) {
	q := r.URL.Query()
	opts := ListInstancesOptions{Search: q.Get("q")}

	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 || limit > maxListInstancesLimit {
			return ListInstancesOptions{}, errors.New("invalid limit")
		}
		opts.Limit = limit
	}
	if raw := q.Get("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return ListInstancesOptions{}, errors.New("invalid offset")
		}
		opts.Offset = offset
	}
	return opts, nil
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
