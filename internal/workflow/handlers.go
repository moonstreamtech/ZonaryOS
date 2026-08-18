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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/apifields"
	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/invoicing"
	"github.com/moonstreamtech/ZonaryOS/internal/queryfilter"
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
	// Part 3a of the multi-tenant isolation hardening + performance batch:
	// the workflow-instance -> invoice cross-link's read side (the
	// instance detail page's own new "Associated invoices" section) - a
	// cross-module read into internal/invoicing.ListInvoicesBySourceInstance,
	// the same "modules own their own tables, callers read through the
	// owning module's exported function" boundary internal/portability's
	// own export already follows.
	mux.Handle("GET /api/firms/{firmID}/workflow-instances/{instanceID}/invoices", auth(http.HandlerFunc(handleInstanceInvoices(pool))))
	// Part 3c: the customer detail page's own "Sales" section reads this -
	// every workflow instance (any definition) whose payload.customer_id
	// matches ?customerId=, see ListInstancesByCustomer's own doc
	// comment. A required query param on the bare collection path, not a
	// nested "/workflow-instances/by-customer/{customerID}" path segment
	// - the same ambiguous-wildcard-vs-literal ServeMux conflict this
	// file's own comment on GET .../workflow-definitions?key= already
	// hit and solved the identical way ("/workflow-instances/{instanceID}"
	// and "/workflow-instances/{instanceID}/invoices" both already exist
	// at that same path depth, and net/http's mux can't tell
	// "/workflow-instances/by-customer/{x}" apart from
	// "/workflow-instances/{instanceID}/invoices" when {x} could be
	// "invoices"). Registered here (not under internal/crm's own
	// /customers/{customerID}/... path) because this query needs
	// internal/workflow's own tables and internal/workflow already
	// imports internal/crm (for FieldTypeCustomer validation) - the
	// reverse import would be a cycle.
	mux.Handle("GET /api/firms/{firmID}/workflow-instances", auth(http.HandlerFunc(handleInstancesByCustomer(pool))))
	// Bulk transition (Part 4 of the multi-tenant isolation hardening +
	// performance batch): see BulkExecuteTransition's own doc comment for
	// the preconditions and atomicity guarantee.
	mux.Handle("POST /api/firms/{firmID}/workflow-instances/bulk-transition", auth(http.HandlerFunc(handleBulkTransition(pool))))
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
	// Approval workflow (Open Points item 41, approvals.go): member-gated
	// list, filtered to approvals the caller can actually resolve;
	// approve/reject each require holding the approval's own permission
	// key, checked inside Approve/Reject themselves.
	mux.Handle("GET /api/firms/{firmID}/pending-approvals", auth(http.HandlerFunc(handleListPendingApprovals(pool))))
	mux.Handle("POST /api/firms/{firmID}/pending-approvals/{id}/approve", auth(http.HandlerFunc(handleApproveApproval(pool))))
	mux.Handle("POST /api/firms/{firmID}/pending-approvals/{id}/reject", auth(http.HandlerFunc(handleRejectApproval(pool))))
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
	// OpenApprovalsCount (Part 3, computed field) is only ever non-nil
	// on handleCurrentState's own response - see
	// InstanceState.OpenApprovalsCount's own doc comment for why
	// ListInstances leaves it unset.
	OpenApprovalsCount *int   `json:"openApprovalsCount,omitempty"`
	UpdatedAt          string `json:"updatedAt"`
}

// instanceStateResponseFields is handleListInstances' own ?fields=
// allowlist (mobile API optimization batch, Part 1).
var instanceStateResponseFields = map[string]bool{
	"instanceId": true, "workflowDefinitionId": true, "state": true, "payload": true,
	"availableActions": true, "openApprovalsCount": true, "updatedAt": true,
}

func toInstanceStateResponse(s InstanceState) instanceStateResponse {
	resp := instanceStateResponse{
		InstanceID:           s.InstanceID.String(),
		WorkflowDefinitionID: s.WorkflowDefinitionID.String(),
		State:                stateInfoResponse{Key: s.State.Key, Name: s.State.Name},
		Payload:              s.Payload,
		AvailableActions:     make([]availableActionResponse, 0, len(s.AvailableActions)),
		OpenApprovalsCount:   s.OpenApprovalsCount,
		UpdatedAt:            s.UpdatedAt.Format(time.RFC3339),
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
//
// inventory.ErrInsufficientStock (Inventory management batch) is included
// here even though it originates from internal/inventory, not this
// package's own sentinel errors - ExecuteTransition's stock-adjustment
// bridge (engine.go) returns it verbatim (no wrapping into a
// workflow-local error) when a sale would take stock below zero, and the
// design brief's own "reject the transition, not just warn" requirement
// means this needs the same clean 400 every other structurally-invalid
// request gets, not a generic 500.
func writeEngineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrDefinitionNotFound), errors.Is(err, ErrInstanceNotFound), errors.Is(err, ErrRuleNotFound), errors.Is(err, ErrApprovalNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrPermissionDenied):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrNoSuchTransition), errors.Is(err, ErrInvalidSpec), errors.Is(err, ErrPayloadValidation), errors.Is(err, ErrInvalidRule),
		errors.Is(err, inventory.ErrInsufficientStock), errors.Is(err, inventory.ErrInvalidStockAdjustment), errors.Is(err, ErrInvalidFilter):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrDefinitionKeyExists), errors.Is(err, ErrApprovalNotPending), errors.Is(err, ErrApprovalExpired):
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

// toJournalTemplateSpec converts a defineJournalTemplateRequest (this
// file's request-side wire shape) into the spec.go JournalTemplate
// DefineWorkflowForFirm/DefineWorkflowTx actually consume - handleDefineWorkflow's
// counterpart to toJournalTemplateResponse below. nil in, nil out -
// DefinitionSpec.Validate already rejects a non-nil JournalTemplate with
// fewer than two lines or a missing field, so no additional validation
// happens here.
func toJournalTemplateSpec(req *defineJournalTemplateRequest) *JournalTemplate {
	if req == nil {
		return nil
	}
	lines := make([]LineTemplate, 0, len(req.Lines))
	for _, l := range req.Lines {
		lines = append(lines, LineTemplate{AccountCode: l.AccountCode, Side: l.Side, AmountField: l.AmountField})
	}
	return &JournalTemplate{Description: req.Description, Lines: lines}
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

// stockAdjustmentTemplateResponse is StockAdjustmentTemplate's (spec.go)
// JSON wire shape - the Inventory management batch's counterpart to
// journalTemplateResponse above, same "included on transitionInfoResponse,
// omitempty" reasoning.
type stockAdjustmentTemplateResponse struct {
	ProductField  string `json:"productField"`
	QuantityField string `json:"quantityField"`
	Direction     string `json:"direction"`
	Reason        string `json:"reason"`
}

// toStockAdjustmentTemplateSpec converts a defineStockAdjustmentTemplateRequest
// into the spec.go StockAdjustmentTemplate DefineWorkflowForFirm/DefineWorkflowTx
// actually consume - handleDefineWorkflow's counterpart to
// toStockAdjustmentTemplateResponse below. nil in, nil out - same contract
// toJournalTemplateSpec uses.
func toStockAdjustmentTemplateSpec(req *defineStockAdjustmentTemplateRequest) *StockAdjustmentTemplate {
	if req == nil {
		return nil
	}
	return &StockAdjustmentTemplate{
		ProductField: req.ProductField, QuantityField: req.QuantityField,
		Direction: req.Direction, Reason: req.Reason,
	}
}

func toStockAdjustmentTemplateResponse(sat *StockAdjustmentTemplate) *stockAdjustmentTemplateResponse {
	if sat == nil {
		return nil
	}
	return &stockAdjustmentTemplateResponse{
		ProductField: sat.ProductField, QuantityField: sat.QuantityField,
		Direction: sat.Direction, Reason: sat.Reason,
	}
}

// deliveryTemplateResponse/customerTemplateResponse are
// DeliveryTemplate/CustomerTemplate's (spec.go) JSON wire shape - the
// Logistics/CRM batch's counterparts to stockAdjustmentTemplateResponse
// above, same "included on transitionInfoResponse, omitempty" reasoning.
type deliveryTemplateResponse struct {
	DestinationAddressField string `json:"destinationAddressField"`
	OriginAddressField      string `json:"originAddressField,omitempty"`
	CarrierField            string `json:"carrierField,omitempty"`
	TrackingNumberField     string `json:"trackingNumberField,omitempty"`
	ReferenceField          string `json:"referenceField,omitempty"`
}

type customerTemplateResponse struct {
	NameField    string `json:"nameField"`
	EmailField   string `json:"emailField,omitempty"`
	PhoneField   string `json:"phoneField,omitempty"`
	AddressField string `json:"addressField,omitempty"`
}

func toDeliveryTemplateSpec(req *defineDeliveryTemplateRequest) *DeliveryTemplate {
	if req == nil {
		return nil
	}
	return &DeliveryTemplate{
		DestinationAddressField: req.DestinationAddressField, OriginAddressField: req.OriginAddressField,
		CarrierField: req.CarrierField, TrackingNumberField: req.TrackingNumberField, ReferenceField: req.ReferenceField,
	}
}

func toDeliveryTemplateResponse(dt *DeliveryTemplate) *deliveryTemplateResponse {
	if dt == nil {
		return nil
	}
	return &deliveryTemplateResponse{
		DestinationAddressField: dt.DestinationAddressField, OriginAddressField: dt.OriginAddressField,
		CarrierField: dt.CarrierField, TrackingNumberField: dt.TrackingNumberField, ReferenceField: dt.ReferenceField,
	}
}

func toCustomerTemplateSpec(req *defineCustomerTemplateRequest) *CustomerTemplate {
	if req == nil {
		return nil
	}
	return &CustomerTemplate{
		NameField: req.NameField, EmailField: req.EmailField, PhoneField: req.PhoneField, AddressField: req.AddressField,
	}
}

func toCustomerTemplateResponse(ct *CustomerTemplate) *customerTemplateResponse {
	if ct == nil {
		return nil
	}
	return &customerTemplateResponse{
		NameField: ct.NameField, EmailField: ct.EmailField, PhoneField: ct.PhoneField, AddressField: ct.AddressField,
	}
}

// invoiceTemplateResponse is InvoiceTemplate's (spec.go) JSON wire shape -
// the Invoicing/payment tracking batch's counterpart to
// deliveryTemplateResponse/customerTemplateResponse above, same "included
// on transitionInfoResponse, omitempty" reasoning.
type invoiceTemplateResponse struct {
	Description    string `json:"description"`
	ProductField   string `json:"productField,omitempty"`
	QuantityField  string `json:"quantityField"`
	UnitPriceField string `json:"unitPriceField"`
	CustomerField  string `json:"customerField,omitempty"`
}

func toInvoiceTemplateSpec(req *defineInvoiceTemplateRequest) *InvoiceTemplate {
	if req == nil {
		return nil
	}
	return &InvoiceTemplate{
		Description: req.Description, ProductField: req.ProductField,
		QuantityField: req.QuantityField, UnitPriceField: req.UnitPriceField, CustomerField: req.CustomerField,
	}
}

func toInvoiceTemplateResponse(it *InvoiceTemplate) *invoiceTemplateResponse {
	if it == nil {
		return nil
	}
	return &invoiceTemplateResponse{
		Description: it.Description, ProductField: it.ProductField,
		QuantityField: it.QuantityField, UnitPriceField: it.UnitPriceField, CustomerField: it.CustomerField,
	}
}

// transitionInfoResponse is TransitionInfo's (engine.go) JSON wire shape.
type transitionInfoResponse struct {
	ActionKey       string                           `json:"actionKey"`
	Name            string                           `json:"name"`
	FromState       stateInfoResponse                `json:"fromState"`
	ToState         stateInfoResponse                `json:"toState"`
	PermissionKey   string                           `json:"permissionKey"`
	Journal         *journalTemplateResponse         `json:"journal,omitempty"`
	StockAdjustment *stockAdjustmentTemplateResponse `json:"stockAdjustment,omitempty"`
	Delivery        *deliveryTemplateResponse        `json:"delivery,omitempty"`
	Customer        *customerTemplateResponse        `json:"customer,omitempty"`
	Invoice         *invoiceTemplateResponse         `json:"invoice,omitempty"`
}

// toTransitionInfoResponses builds the HTTP-facing transitionInfoResponse
// list from TransitionInfo's own generic Effects slice - extracting each
// bridge kind out of Effects (extractEffect, engine.go) to populate the
// exact same journal/stockAdjustment/delivery/customer/invoice response
// fields the API returned before the Effects refactor. An extractEffect
// error here (a stored Payload that fails to unmarshal into its own
// Kind's struct) can only happen for data this same package itself wrote
// via marshalEffects/JournalEffect etc., so it's treated as an internal
// error rather than surfaced per-field - logged and the field left nil,
// the same "don't fail an entire definition listing over one malformed
// legacy row" resilience every other read path in this file already
// favors over a hard error.
func toTransitionInfoResponses(transitions []TransitionInfo) []transitionInfoResponse {
	if len(transitions) == 0 {
		return nil
	}
	resp := make([]transitionInfoResponse, 0, len(transitions))
	for _, t := range transitions {
		journal, err := extractEffect[JournalTemplate](t.Effects, EffectKindJournal)
		if err != nil {
			journal = nil
		}
		stockAdjustment, err := extractEffect[StockAdjustmentTemplate](t.Effects, EffectKindStock)
		if err != nil {
			stockAdjustment = nil
		}
		delivery, err := extractEffect[DeliveryTemplate](t.Effects, EffectKindDelivery)
		if err != nil {
			delivery = nil
		}
		customer, err := extractEffect[CustomerTemplate](t.Effects, EffectKindCustomer)
		if err != nil {
			customer = nil
		}
		invoice, err := extractEffect[InvoiceTemplate](t.Effects, EffectKindInvoice)
		if err != nil {
			invoice = nil
		}
		resp = append(resp, transitionInfoResponse{
			ActionKey:       t.ActionKey,
			Name:            t.Name,
			FromState:       stateInfoResponse{Key: t.FromState.Key, Name: t.FromState.Name},
			ToState:         stateInfoResponse{Key: t.ToState.Key, Name: t.ToState.Name},
			PermissionKey:   t.PermissionKey,
			Journal:         toJournalTemplateResponse(journal),
			StockAdjustment: toStockAdjustmentTemplateResponse(stockAdjustment),
			Delivery:        toDeliveryTemplateResponse(delivery),
			Customer:        toCustomerTemplateResponse(customer),
			Invoice:         toInvoiceTemplateResponse(invoice),
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
	// Journal is OPTIONAL (the financial management core's workflow-to-
	// ledger bridge, spec.go's TransitionSpec.Journal) - mirrors
	// lineTemplateResponse/journalTemplateResponse's shape (this file's
	// response side) on the request side, the same "separate request/
	// response struct pair" convention defineFieldRequest/fieldSpecResponse
	// already use. Omitted entirely (or nil) means "post nothing", exactly
	// TransitionSpec.Journal's own zero-value contract.
	Journal *defineJournalTemplateRequest `json:"journal,omitempty"`
	// StockAdjustment is OPTIONAL (the Inventory management batch's
	// workflow-to-inventory bridge, spec.go's TransitionSpec.StockAdjustment) -
	// same "separate request/response struct pair" convention Journal's
	// own doc comment above describes. Omitted entirely (or nil) means
	// "adjust nothing", exactly TransitionSpec.StockAdjustment's own
	// zero-value contract.
	StockAdjustment *defineStockAdjustmentTemplateRequest `json:"stockAdjustment,omitempty"`
	// Delivery/Customer are OPTIONAL (the Logistics/CRM batch's
	// workflow-to-logistics/workflow-to-CRM bridges, spec.go's
	// TransitionSpec.Delivery/Customer) - same "separate request/response
	// struct pair" convention Journal's own doc comment above describes.
	// Omitted entirely (or nil) means "create nothing", exactly
	// TransitionSpec.Delivery/Customer's own zero-value contract.
	Delivery *defineDeliveryTemplateRequest `json:"delivery,omitempty"`
	Customer *defineCustomerTemplateRequest `json:"customer,omitempty"`
	// Invoice is OPTIONAL (the Invoicing/payment tracking batch's
	// workflow-to-invoicing bridge, spec.go's TransitionSpec.Invoice) -
	// same "separate request/response struct pair" convention Journal's
	// own doc comment above describes. Omitted entirely (or nil) means
	// "create nothing", exactly TransitionSpec.Invoice's own zero-value
	// contract.
	Invoice *defineInvoiceTemplateRequest `json:"invoice,omitempty"`
}

type defineLineTemplateRequest struct {
	AccountCode string `json:"accountCode"`
	Side        string `json:"side"`
	AmountField string `json:"amountField"`
}

type defineJournalTemplateRequest struct {
	Description string                      `json:"description"`
	Lines       []defineLineTemplateRequest `json:"lines"`
}

// defineStockAdjustmentTemplateRequest is StockAdjustmentTemplate's
// (spec.go) JSON wire shape on the request side - mirrors
// stockAdjustmentTemplateResponse's shape (this file's response side), the
// same "separate request/response struct pair" convention
// defineJournalTemplateRequest/journalTemplateResponse already use.
type defineStockAdjustmentTemplateRequest struct {
	ProductField  string `json:"productField"`
	QuantityField string `json:"quantityField"`
	Direction     string `json:"direction"`
	Reason        string `json:"reason"`
}

// defineDeliveryTemplateRequest/defineCustomerTemplateRequest are
// DeliveryTemplate/CustomerTemplate's (spec.go) JSON wire shape on the
// request side - mirror deliveryTemplateResponse/customerTemplateResponse's
// shape (this file's response side), the same "separate request/response
// struct pair" convention every other template in this file already uses.
type defineDeliveryTemplateRequest struct {
	DestinationAddressField string `json:"destinationAddressField"`
	OriginAddressField      string `json:"originAddressField"`
	CarrierField            string `json:"carrierField"`
	TrackingNumberField     string `json:"trackingNumberField"`
	ReferenceField          string `json:"referenceField"`
}

type defineCustomerTemplateRequest struct {
	NameField    string `json:"nameField"`
	EmailField   string `json:"emailField"`
	PhoneField   string `json:"phoneField"`
	AddressField string `json:"addressField"`
}

// defineInvoiceTemplateRequest is InvoiceTemplate's (spec.go) JSON wire
// shape on the request side - mirrors invoiceTemplateResponse's shape
// (this file's response side), the same "separate request/response
// struct pair" convention every other template in this file already uses.
type defineInvoiceTemplateRequest struct {
	Description    string `json:"description"`
	ProductField   string `json:"productField"`
	QuantityField  string `json:"quantityField"`
	UnitPriceField string `json:"unitPriceField"`
	CustomerField  string `json:"customerField"`
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
			// Builds Effects from whichever of the request's own named
			// journal/stockAdjustment/delivery/customer/invoice fields are
			// present - the request JSON shape itself is unchanged by the
			// Effects refactor (see TransitionSpec's own doc comment,
			// spec.go); only the internal TransitionSpec this becomes is
			// now a slice rather than five named fields.
			var effects []TransitionEffect
			if jt := toJournalTemplateSpec(t.Journal); jt != nil {
				effects = append(effects, JournalEffect(*jt))
			}
			if sat := toStockAdjustmentTemplateSpec(t.StockAdjustment); sat != nil {
				effects = append(effects, StockEffect(*sat))
			}
			if dt := toDeliveryTemplateSpec(t.Delivery); dt != nil {
				effects = append(effects, DeliveryEffect(*dt))
			}
			if ct := toCustomerTemplateSpec(t.Customer); ct != nil {
				effects = append(effects, CustomerEffect(*ct))
			}
			if it := toInvoiceTemplateSpec(t.Invoice); it != nil {
				effects = append(effects, InvoiceEffect(*it))
			}
			spec.Transitions = append(spec.Transitions, TransitionSpec{
				FromStateKey: t.FromStateKey,
				ToStateKey:   t.ToStateKey,
				ActionKey:    t.ActionKey,
				Name:         t.Name,
				Permission: PermissionSpec{
					Key:         t.Permission.Key,
					Description: t.Permission.Description,
				},
				Effects: effects,
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

		projected, err := apifields.Project(resp, apifields.ParseFields(r.URL.Query().Get("fields")), instanceStateResponseFields)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(result.Total))
		w.Header().Set("Content-Type", "application/json")
		// #nosec G705 -- see internal/inventory.handleListProducts' own
		// identical suppression comment; projected is self-produced JSON
		// filtered to instanceStateResponseFields' own closed allowlist.
		_, _ = w.Write(projected)
	}
}

// parseListInstancesOptions reads ?limit=/?offset=/?q=/?filters= off r
// into ListInstancesOptions, rejecting a negative or over-cap
// limit/offset outright rather than silently clamping - a caller sending
// garbage should see a 400, not a quietly "corrected" page. ?filters= is
// a JSON-encoded `[{field, op, value}, ...]` array (Part 2 of the
// search/filtering/enrichment batch, internal/queryfilter.Filter) -
// malformed JSON here is a 400 too, same failure mode as a bad limit.
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
	filters, err := queryfilter.ParseFiltersParam(q.Get("filters"))
	if err != nil {
		return ListInstancesOptions{}, err
	}
	// ?updated_since= (mobile API optimization batch, Part 1) is a
	// friendlier alias for the equivalent ?filters= entry against
	// workflowInstanceFilterFields' own "updated_at" column - see
	// internal/invoicing.handleListInvoices' identical translation.
	if raw := q.Get("updated_since"); raw != "" {
		if _, err := apifields.ParseUpdatedSince(raw); err != nil {
			return ListInstancesOptions{}, err
		}
		filters = append(filters, queryfilter.Filter{Field: "updated_at", Op: queryfilter.OpGte, Value: raw})
	}
	opts.Filters = filters
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

// instanceInvoiceResponse is a summary shape (no lines) - enough for the
// instance detail page's own "Associated invoices" section to render a
// list with a link to each invoice's own detail page.
type instanceInvoiceResponse struct {
	ID            string `json:"id"`
	InvoiceNumber string `json:"invoiceNumber"`
	Status        string `json:"status"`
	Total         string `json:"total"`
	Currency      string `json:"currency"`
}

// handleInstanceInvoices serves GET .../workflow-instances/{instanceID}/invoices
// - every invoice whose source_workflow_instance is instanceID (Part 3a).
func handleInstanceInvoices(pool *pgxpool.Pool) http.HandlerFunc {
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

		invoices, err := invoicing.ListInvoicesBySourceInstance(r.Context(), pool, firmID, userID, instanceID)
		if err != nil {
			if errors.Is(err, invoicing.ErrFirmNotFound) {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		resp := make([]instanceInvoiceResponse, 0, len(invoices))
		for _, inv := range invoices {
			resp = append(resp, instanceInvoiceResponse{
				ID: inv.ID.String(), InvoiceNumber: inv.InvoiceNumber, Status: string(inv.Status),
				Total: inv.Total, Currency: inv.Currency,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// instanceSummaryResponse mirrors InstanceSummary's wire shape.
type instanceSummaryResponse struct {
	ID             string `json:"id"`
	DefinitionKey  string `json:"definitionKey"`
	DefinitionName string `json:"definitionName"`
	StateKey       string `json:"stateKey"`
	StateName      string `json:"stateName"`
	CreatedAt      string `json:"createdAt"`
}

// handleInstancesByCustomer serves GET .../workflow-instances?customerId=
// (Part 3c) - customerId is required for now (the only bare
// GET .../workflow-instances query this collection path supports); a
// future caller wanting some other filter shape is real future work, not
// something this handler needs to anticipate today.
func handleInstancesByCustomer(pool *pgxpool.Pool) http.HandlerFunc {
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
		rawCustomerID := r.URL.Query().Get("customerId")
		if rawCustomerID == "" {
			http.Error(w, "customerId query param is required", http.StatusBadRequest)
			return
		}
		customerID, err := uuid.Parse(rawCustomerID)
		if err != nil {
			http.Error(w, "invalid customer id", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		summaries, err := ListInstancesByCustomer(r.Context(), pool, firmID, userID, customerID)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		resp := make([]instanceSummaryResponse, 0, len(summaries))
		for _, s := range summaries {
			resp = append(resp, instanceSummaryResponse{
				ID: s.ID.String(), DefinitionKey: s.DefinitionKey, DefinitionName: s.DefinitionName,
				StateKey: s.StateKey, StateName: s.StateName, CreatedAt: s.CreatedAt.Format(time.RFC3339),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type bulkTransitionRequest struct {
	InstanceIDs []string       `json:"instanceIds"`
	ActionKey   string         `json:"actionKey"`
	Payload     map[string]any `json:"payload"`
}

type bulkTransitionResponse struct {
	InstanceIDs []string `json:"instanceIds"`
	ToStateKey  string   `json:"toStateKey"`
}

// handleBulkTransition serves POST .../workflow-instances/bulk-transition
// (Part 4) - see BulkExecuteTransition's own doc comment.
func handleBulkTransition(pool *pgxpool.Pool) http.HandlerFunc {
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

		var req bulkTransitionRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.ActionKey == "" {
			http.Error(w, "actionKey is required", http.StatusBadRequest)
			return
		}
		instanceIDs := make([]uuid.UUID, 0, len(req.InstanceIDs))
		for _, raw := range req.InstanceIDs {
			instanceID, err := uuid.Parse(raw)
			if err != nil {
				http.Error(w, "invalid instance id in instanceIds", http.StatusBadRequest)
				return
			}
			instanceIDs = append(instanceIDs, instanceID)
		}

		result, err := BulkExecuteTransition(r.Context(), pool, firmID, userID, instanceIDs, req.ActionKey, req.Payload)
		if err != nil {
			switch {
			case errors.Is(err, ErrBulkTransitionEmpty), errors.Is(err, ErrBulkTransitionMixedDefinitions):
				http.Error(w, err.Error(), http.StatusBadRequest)
			default:
				writeEngineError(w, err)
			}
			return
		}

		resp := bulkTransitionResponse{ToStateKey: result.ToStateKey, InstanceIDs: make([]string, 0, len(result.InstanceIDs))}
		for _, id := range result.InstanceIDs {
			resp.InstanceIDs = append(resp.InstanceIDs, id.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
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
	ID               string                 `json:"id"`
	DefinitionKey    string                 `json:"definitionKey"`
	Name             string                 `json:"name"`
	Trigger          string                 `json:"trigger"`
	ConditionTree    expressionNodeResponse `json:"conditionTree"`
	Actions          []actionResponse       `json:"actions"`
	Autonomous       bool                   `json:"autonomous"`
	Enabled          bool                   `json:"enabled"`
	ScheduleInterval *string                `json:"scheduleInterval,omitempty"`
}

func toRuleResponse(r Rule) ruleResponse {
	resp := ruleResponse{
		ID:               r.ID.String(),
		DefinitionKey:    r.DefinitionKey,
		Name:             r.Name,
		Trigger:          string(r.Trigger),
		ScheduleInterval: r.ScheduleInterval,
		ConditionTree:    toExpressionNodeResponse(r.ConditionTree),
		Autonomous:       r.Autonomous,
		Enabled:          r.Enabled,
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
	Name             string                `json:"name"`
	Trigger          string                `json:"trigger"`
	ConditionTree    expressionNodeRequest `json:"conditionTree"`
	Actions          []actionRequest       `json:"actions"`
	Autonomous       bool                  `json:"autonomous"`
	Enabled          bool                  `json:"enabled"`
	ScheduleInterval *string               `json:"scheduleInterval,omitempty"`
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
			Name:             req.Name,
			Trigger:          Trigger(req.Trigger),
			ConditionTree:    req.ConditionTree.toExpressionNode(),
			Actions:          actions,
			Autonomous:       req.Autonomous,
			Enabled:          req.Enabled,
			ScheduleInterval: req.ScheduleInterval,
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
	Name             *string                `json:"name,omitempty"`
	Enabled          *bool                  `json:"enabled,omitempty"`
	Autonomous       *bool                  `json:"autonomous,omitempty"`
	ConditionTree    *expressionNodeRequest `json:"conditionTree,omitempty"`
	Actions          *[]actionRequest       `json:"actions,omitempty"`
	ScheduleInterval *string                `json:"scheduleInterval,omitempty"`
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

		patch := RuleUpdate{Name: req.Name, Enabled: req.Enabled, Autonomous: req.Autonomous, ScheduleInterval: req.ScheduleInterval}
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

// pendingApprovalResponse is PendingApproval's JSON wire shape.
type pendingApprovalResponse struct {
	ID                string  `json:"id"`
	InstanceID        string  `json:"instanceId"`
	ProposedActionKey string  `json:"proposedActionKey"`
	PermissionKey     string  `json:"permissionKey"`
	RequestedBy       string  `json:"requestedBy"`
	RuleID            *string `json:"ruleId,omitempty"`
	Status            string  `json:"status"`
	ResolvedBy        *string `json:"resolvedBy,omitempty"`
	ResolvedAt        *string `json:"resolvedAt,omitempty"`
	ExpiresAt         *string `json:"expiresAt,omitempty"`
	CreatedAt         string  `json:"createdAt"`
}

func toPendingApprovalResponse(a PendingApproval) pendingApprovalResponse {
	resp := pendingApprovalResponse{
		ID: a.ID.String(), InstanceID: a.InstanceID.String(), ProposedActionKey: a.ProposedActionKey,
		PermissionKey: a.PermissionKey, RequestedBy: a.RequestedBy.String(), Status: string(a.Status),
		ExpiresAt: formatTimePtr(a.ExpiresAt), ResolvedAt: formatTimePtr(a.ResolvedAt), CreatedAt: a.CreatedAt.Format(time.RFC3339),
	}
	if a.RuleID != nil {
		s := a.RuleID.String()
		resp.RuleID = &s
	}
	if a.ResolvedBy != nil {
		s := a.ResolvedBy.String()
		resp.ResolvedBy = &s
	}
	return resp
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

// handleListPendingApprovals serves GET .../pending-approvals -
// member-gated, filtered to approvals the caller could actually resolve
// (holds the proposed action's permission key) - see
// ListPendingApprovals' own doc comment.
func handleListPendingApprovals(pool *pgxpool.Pool) http.HandlerFunc {
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

		approvals, err := ListPendingApprovals(r.Context(), pool, firmID, userID)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		resp := make([]pendingApprovalResponse, 0, len(approvals))
		for _, a := range approvals {
			resp = append(resp, toPendingApprovalResponse(a))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// handleApproveApproval serves POST .../pending-approvals/{id}/approve -
// executes the proposed transition for real (Approve's own doc comment).
func handleApproveApproval(pool *pgxpool.Pool) http.HandlerFunc {
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
		approvalID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid approval id", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		approved, err := Approve(r.Context(), pool, firmID, userID, approvalID)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPendingApprovalResponse(approved))
	}
}

// handleRejectApproval serves POST .../pending-approvals/{id}/reject -
// marks the approval rejected and notifies whoever requested it (Reject's
// own doc comment).
func handleRejectApproval(pool *pgxpool.Pool) http.HandlerFunc {
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
		approvalID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid approval id", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		rejected, err := Reject(r.Context(), pool, firmID, userID, approvalID)
		if err != nil {
			writeEngineError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPendingApprovalResponse(rejected))
	}
}
