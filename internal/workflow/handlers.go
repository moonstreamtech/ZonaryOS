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
	// Read-only rule listing (Open Points item 7's HTTP surface for this
	// batch): a rule IS visible in the UI (e.g. on the workflow definition
	// page), even though the rule-creation form itself is deferred to a
	// later batch - see internal/workflow/rules.go's ListRulesForDefinition.
	mux.Handle("GET /api/firms/{firmID}/workflow-definitions/{definitionKey}/rules", auth(http.HandlerFunc(handleListRules(pool))))
	// Rule creation/update/deletion (this batch's Rule Engine Builder UI
	// backend): owner-gated, see CreateRuleForFirm/UpdateRuleForFirm/
	// DeleteRuleForFirm's own doc comments (rules.go).
	mux.Handle("POST /api/firms/{firmID}/workflow-definitions/{definitionKey}/rules", auth(http.HandlerFunc(handleCreateRule(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/workflow-definitions/{definitionKey}/rules/{ruleID}", auth(http.HandlerFunc(handleUpdateRule(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/workflow-definitions/{definitionKey}/rules/{ruleID}", auth(http.HandlerFunc(handleDeleteRule(pool))))
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
	case errors.Is(err, ErrDefinitionNotFound), errors.Is(err, ErrInstanceNotFound), errors.Is(err, ErrRuleNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrPermissionDenied):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrNoSuchTransition), errors.Is(err, ErrInvalidSpec), errors.Is(err, ErrPayloadValidation), errors.Is(err, ErrInvalidRule):
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

// lineTemplateResponse/journalTemplateResponse are LineTemplate/
// JournalTemplate's (spec.go) JSON wire shape - included on
// transitionInfoResponse (below) so the workflow definition page can
// render the journal template each transition carries, read-only, without
// a second endpoint. omitempty on transitionInfoResponse.Journal means a
// transition with no template (every transition defined before this
// batch) serializes with no "journal" key at all - purely additive, same
// convention fieldSpecResponse's own doc comment establishes for item 35.
type lineTemplateResponse struct {
	AccountCode string `json:"accountCode"`
	Side        string `json:"side"`
	AmountField string `json:"amountField"`
}

type journalTemplateResponse struct {
	Description string                 `json:"description"`
	Lines       []lineTemplateResponse `json:"lines"`
}

func toJournalTemplateResponse(jt *JournalTemplate) *journalTemplateResponse {
	if jt == nil {
		return nil
	}
	lines := make([]lineTemplateResponse, 0, len(jt.Lines))
	for _, l := range jt.Lines {
		lines = append(lines, lineTemplateResponse{AccountCode: l.AccountCode, Side: l.Side, AmountField: l.AmountField})
	}
	return &journalTemplateResponse{Description: jt.Description, Lines: lines}
}

// transitionInfoResponse is TransitionInfo's (engine.go) JSON wire shape.
type transitionInfoResponse struct {
	ActionKey     string                   `json:"actionKey"`
	Name          string                   `json:"name"`
	FromState     stateInfoResponse        `json:"fromState"`
	ToState       stateInfoResponse        `json:"toState"`
	PermissionKey string                   `json:"permissionKey"`
	Journal       *journalTemplateResponse `json:"journal,omitempty"`
}

func toTransitionInfoResponses(transitions []TransitionInfo) []transitionInfoResponse {
	if len(transitions) == 0 {
		return nil
	}
	resp := make([]transitionInfoResponse, 0, len(transitions))
	for _, t := range transitions {
		resp = append(resp, transitionInfoResponse{
			ActionKey:     t.ActionKey,
			Name:          t.Name,
			FromState:     stateInfoResponse{Key: t.FromState.Key, Name: t.FromState.Name},
			ToState:       stateInfoResponse{Key: t.ToState.Key, Name: t.ToState.Name},
			PermissionKey: t.PermissionKey,
			Journal:       toJournalTemplateResponse(t.Journal),
		})
	}
	return resp
}

type definitionInfoResponse struct {
	DefinitionID        string                   `json:"definitionId"`
	Key                 string                   `json:"key"`
	Name                string                   `json:"name"`
	CreatePermissionKey string                   `json:"createPermissionKey"`
	Fields              []fieldSpecResponse      `json:"fields,omitempty"`
	Transitions         []transitionInfoResponse `json:"transitions,omitempty"`
}

func toDefinitionInfoResponse(info DefinitionInfo) definitionInfoResponse {
	return definitionInfoResponse{
		DefinitionID:        info.ID.String(),
		Key:                 info.Key,
		Name:                info.Name,
		CreatePermissionKey: info.CreatePermissionKey,
		Fields:              toFieldSpecResponses(info.Fields),
		Transitions:         toTransitionInfoResponses(info.Transitions),
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

// expressionNodeResponse/actionResponse are ExpressionNode/Action's
// (rules.go) JSON wire shapes - both types already carry `json:"...,omitempty"`
// tags fit for direct API exposure, so these are 1:1 field-for-field
// mirrors, kept as their own types only for the same reason every other
// response type in this file is separate from its internal/workflow
// counterpart (e.g. stateInfoResponse vs. StateInfo) rather than
// exporting the internal type directly over the wire.
type expressionNodeResponse struct {
	Op              string                   `json:"op,omitempty"`
	Children        []expressionNodeResponse `json:"children,omitempty"`
	Type            string                   `json:"type,omitempty"`
	Field           string                   `json:"field,omitempty"`
	Value           any                      `json:"value,omitempty"`
	DefinitionKey   string                   `json:"definitionKey,omitempty"`
	InstanceIDField string                   `json:"instanceIdField,omitempty"`
	State           string                   `json:"state,omitempty"`
	FieldA          string                   `json:"fieldA,omitempty"`
	FieldB          string                   `json:"fieldB,omitempty"`
}

func toExpressionNodeResponse(n ExpressionNode) expressionNodeResponse {
	resp := expressionNodeResponse{
		Op:              n.Op,
		Type:            string(n.Type),
		Field:           n.Field,
		Value:           n.Value,
		DefinitionKey:   n.DefinitionKey,
		InstanceIDField: n.InstanceIDField,
		State:           n.State,
		FieldA:          n.FieldA,
		FieldB:          n.FieldB,
	}
	for _, c := range n.Children {
		resp.Children = append(resp.Children, toExpressionNodeResponse(c))
	}
	return resp
}

type actionResponse struct {
	Type            string `json:"type"`
	ActionKey       string `json:"actionKey,omitempty"`
	Channel         string `json:"channel,omitempty"`
	MessageTemplate string `json:"messageTemplate,omitempty"`
	Field           string `json:"field,omitempty"`
	Value           any    `json:"value,omitempty"`
}

func toActionResponse(a Action) actionResponse {
	return actionResponse{
		Type:            string(a.Type),
		ActionKey:       a.ActionKey,
		Channel:         string(a.Channel),
		MessageTemplate: a.MessageTemplate,
		Field:           a.Field,
		Value:           a.Value,
	}
}

type ruleResponse struct {
	ID            string                 `json:"id"`
	DefinitionKey string                 `json:"definitionKey"`
	Name          string                 `json:"name"`
	Trigger       string                 `json:"trigger"`
	ConditionTree expressionNodeResponse `json:"conditionTree"`
	Actions       []actionResponse       `json:"actions"`
	Autonomous    bool                   `json:"autonomous"`
	Enabled       bool                   `json:"enabled"`
}

func toRuleResponse(r Rule) ruleResponse {
	resp := ruleResponse{
		ID:            r.ID.String(),
		DefinitionKey: r.DefinitionKey,
		Name:          r.Name,
		Trigger:       string(r.Trigger),
		ConditionTree: toExpressionNodeResponse(r.ConditionTree),
		Autonomous:    r.Autonomous,
		Enabled:       r.Enabled,
	}
	for _, a := range r.Actions {
		resp.Actions = append(resp.Actions, toActionResponse(a))
	}
	return resp
}

// handleListRules serves GET .../workflow-definitions/{definitionKey}/rules
// - the minimal read-only listing endpoint this batch adds so a rule IS
// visible in the UI (e.g. on the workflow definition page) even though the
// rule-creation form is deferred to a later batch. Membership-checked
// (ListRulesForDefinition), not filtered by any rule-specific permission -
// there is no rule-management permission tier yet (that's part of the
// deferred builder UI's own design).
func handleListRules(pool *pgxpool.Pool) http.HandlerFunc {
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
		definitionKey := r.PathValue("definitionKey")

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		rules, err := ListRulesForDefinition(r.Context(), pool, firmID, userID, definitionKey)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		resp := make([]ruleResponse, 0, len(rules))
		for _, rule := range rules {
			resp = append(resp, toRuleResponse(rule))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// expressionNodeRequest/actionRequest are ExpressionNode/Action's JSON
// wire shape for the request side - mirror expressionNodeResponse/
// actionResponse's own fields (this file's response side) so the wire
// format round-trips, kept as separate types the same way defineFieldRequest
// stays separate from fieldSpecResponse elsewhere in this file.
type expressionNodeRequest struct {
	Op              string                  `json:"op,omitempty"`
	Children        []expressionNodeRequest `json:"children,omitempty"`
	Type            string                  `json:"type,omitempty"`
	Field           string                  `json:"field,omitempty"`
	Value           any                     `json:"value,omitempty"`
	DefinitionKey   string                  `json:"definitionKey,omitempty"`
	InstanceIDField string                  `json:"instanceIdField,omitempty"`
	State           string                  `json:"state,omitempty"`
	FieldA          string                  `json:"fieldA,omitempty"`
	FieldB          string                  `json:"fieldB,omitempty"`
}

func (n expressionNodeRequest) toExpressionNode() ExpressionNode {
	node := ExpressionNode{
		Op:              n.Op,
		Type:            ConditionType(n.Type),
		Field:           n.Field,
		Value:           n.Value,
		DefinitionKey:   n.DefinitionKey,
		InstanceIDField: n.InstanceIDField,
		State:           n.State,
		FieldA:          n.FieldA,
		FieldB:          n.FieldB,
	}
	for _, c := range n.Children {
		node.Children = append(node.Children, c.toExpressionNode())
	}
	return node
}

type actionRequest struct {
	Type            string `json:"type"`
	ActionKey       string `json:"actionKey,omitempty"`
	Channel         string `json:"channel,omitempty"`
	MessageTemplate string `json:"messageTemplate,omitempty"`
	Field           string `json:"field,omitempty"`
	Value           any    `json:"value,omitempty"`
}

func (a actionRequest) toAction() Action {
	return Action{
		Type:            ActionType(a.Type),
		ActionKey:       a.ActionKey,
		Channel:         NotifyChannel(a.Channel),
		MessageTemplate: a.MessageTemplate,
		Field:           a.Field,
		Value:           a.Value,
	}
}

// createRuleRequest is CreateRuleForFirm's request body - a 1:1 mapping
// of Rule's caller-supplied fields (ID/FirmID/DefinitionKey/CreatedAt are
// all server-assigned or path-derived, not part of the request body).
type createRuleRequest struct {
	Name          string                `json:"name"`
	Trigger       string                `json:"trigger"`
	ConditionTree expressionNodeRequest `json:"conditionTree"`
	Actions       []actionRequest       `json:"actions"`
	Autonomous    bool                  `json:"autonomous"`
	Enabled       bool                  `json:"enabled"`
}

// handleCreateRule serves POST .../workflow-definitions/{definitionKey}/rules
// - creates a rule for the firm's own workflow definition, owner-gated
// (CreateRuleForFirm). Structural validation (ValidateExpressionTree/
// ValidateActions) and the obvious-self-referential-loop check both map to
// ErrInvalidRule -> 400 via writeEngineError, not a generic 500.
func handleCreateRule(pool *pgxpool.Pool) http.HandlerFunc {
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
		definitionKey := r.PathValue("definitionKey")

		var req createRuleRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		actions := make([]Action, 0, len(req.Actions))
		for _, a := range req.Actions {
			actions = append(actions, a.toAction())
		}
		rule := Rule{
			Name:          req.Name,
			Trigger:       Trigger(req.Trigger),
			ConditionTree: req.ConditionTree.toExpressionNode(),
			Actions:       actions,
			Autonomous:    req.Autonomous,
			Enabled:       req.Enabled,
		}

		created, err := CreateRuleForFirm(r.Context(), pool, firmID, userID, definitionKey, rule)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toRuleResponse(created))
	}
}

// updateRuleRequest is UpdateRuleForFirm's request body - every field is a
// pointer (present-but-null vs. absent both leave the field untouched,
// same "partial update" contract RuleUpdate itself documents), except
// Enabled which is the "enabled/disabled toggle" the design brief calls
// out by name; kept as *bool like the rest for the same optional-field
// convention.
type updateRuleRequest struct {
	Name          *string                `json:"name,omitempty"`
	Enabled       *bool                  `json:"enabled,omitempty"`
	Autonomous    *bool                  `json:"autonomous,omitempty"`
	ConditionTree *expressionNodeRequest `json:"conditionTree,omitempty"`
	Actions       *[]actionRequest       `json:"actions,omitempty"`
}

// handleUpdateRule serves PATCH .../workflow-definitions/{definitionKey}/rules/{ruleID}
// - owner-gated (UpdateRuleForFirm), same validation/error-mapping as
// handleCreateRule.
func handleUpdateRule(pool *pgxpool.Pool) http.HandlerFunc {
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
		definitionKey := r.PathValue("definitionKey")
		ruleID, err := uuid.Parse(r.PathValue("ruleID"))
		if err != nil {
			http.Error(w, "invalid rule id", http.StatusBadRequest)
			return
		}

		var req updateRuleRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		patch := RuleUpdate{Name: req.Name, Enabled: req.Enabled, Autonomous: req.Autonomous}
		if req.ConditionTree != nil {
			tree := req.ConditionTree.toExpressionNode()
			patch.ConditionTree = &tree
		}
		if req.Actions != nil {
			actions := make([]Action, 0, len(*req.Actions))
			for _, a := range *req.Actions {
				actions = append(actions, a.toAction())
			}
			patch.Actions = &actions
		}

		updated, err := UpdateRuleForFirm(r.Context(), pool, firmID, userID, ruleID, definitionKey, patch)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toRuleResponse(updated))
	}
}

// handleDeleteRule serves DELETE .../workflow-definitions/{definitionKey}/rules/{ruleID}
// - owner-gated (DeleteRuleForFirm).
func handleDeleteRule(pool *pgxpool.Pool) http.HandlerFunc {
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
		definitionKey := r.PathValue("definitionKey")
		ruleID, err := uuid.Parse(r.PathValue("ruleID"))
		if err != nil {
			http.Error(w, "invalid rule id", http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		if err := DeleteRuleForFirm(r.Context(), pool, firmID, userID, ruleID, definitionKey); err != nil {
			writeEngineError(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
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
