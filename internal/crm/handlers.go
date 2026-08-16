// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package crm

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

// RegisterRoutes wires crm's HTTP endpoints into mux, same bearer-token
// auth middleware and /api/firms/{firmID}/... convention as every other
// firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/customers", auth(http.HandlerFunc(handleListCustomers(pool))))
	mux.Handle("POST /api/firms/{firmID}/customers", auth(http.HandlerFunc(handleCreateCustomer(pool))))
	mux.Handle("GET /api/firms/{firmID}/customers/{customerID}", auth(http.HandlerFunc(handleGetCustomer(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/customers/{customerID}", auth(http.HandlerFunc(handleUpdateCustomer(pool))))
	mux.Handle("GET /api/firms/{firmID}/customers/{customerID}/timeline", auth(http.HandlerFunc(handleGetCustomerTimeline(pool))))
	mux.Handle("GET /api/firms/{firmID}/customers/{customerID}/interactions", auth(http.HandlerFunc(handleListInteractions(pool))))
	mux.Handle("POST /api/firms/{firmID}/customers/{customerID}/interactions", auth(http.HandlerFunc(handleCreateInteraction(pool))))

	mux.Handle("GET /api/firms/{firmID}/opportunities", auth(http.HandlerFunc(handleListOpportunities(pool))))
	mux.Handle("POST /api/firms/{firmID}/opportunities", auth(http.HandlerFunc(handleCreateOpportunity(pool))))
	mux.Handle("GET /api/firms/{firmID}/opportunities/{opportunityID}", auth(http.HandlerFunc(handleGetOpportunity(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/opportunities/{opportunityID}", auth(http.HandlerFunc(handleUpdateOpportunity(pool))))
	mux.Handle("POST /api/firms/{firmID}/opportunities/{opportunityID}/stage", auth(http.HandlerFunc(handleUpdateOpportunityStage(pool))))
}

// writeCRMError maps this package's sentinel errors to the HTTP status
// that reflects why the caller isn't getting what they asked for - same
// convention as internal/inventory's writeInventoryError.
func writeCRMError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrCustomerNotFound), errors.Is(err, ErrInteractionNotFound), errors.Is(err, ErrOpportunityNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidCustomer), errors.Is(err, ErrInvalidInteraction), errors.Is(err, ErrInvalidOpportunity), errors.Is(err, ErrInvalidOpportunityTransition):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type customerResponse struct {
	ID                     string         `json:"id"`
	Name                   string         `json:"name"`
	Email                  *string        `json:"email,omitempty"`
	Phone                  *string        `json:"phone,omitempty"`
	Address                *string        `json:"address,omitempty"`
	TaxID                  *string        `json:"taxId,omitempty"`
	CreditLimit            *string        `json:"creditLimit,omitempty"`
	Currency               string         `json:"currency"`
	CustomFields           map[string]any `json:"customFields"`
	SourceWorkflowInstance *string        `json:"sourceWorkflowInstance,omitempty"`
	CreatedAt              string         `json:"createdAt"`
	// TotalInvoiced/TotalPaid (Part 3, computed fields) are only ever
	// non-nil on handleGetCustomer's own response - see
	// Customer.TotalInvoiced's own doc comment for why ListCustomers
	// leaves them unset.
	TotalInvoiced *string `json:"totalInvoiced,omitempty"`
	TotalPaid     *string `json:"totalPaid,omitempty"`
}

func toCustomerResponse(c Customer) customerResponse {
	var sourceWorkflowInstance *string
	if c.SourceWorkflowInstance != nil {
		s := c.SourceWorkflowInstance.String()
		sourceWorkflowInstance = &s
	}
	return customerResponse{
		ID: c.ID.String(), Name: c.Name, Email: c.Email, Phone: c.Phone, Address: c.Address, TaxID: c.TaxID,
		CreditLimit: c.CreditLimit, Currency: c.Currency, CustomFields: c.CustomFields,
		SourceWorkflowInstance: sourceWorkflowInstance, CreatedAt: c.CreatedAt.Format(time.RFC3339),
		TotalInvoiced: c.TotalInvoiced, TotalPaid: c.TotalPaid,
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

func handleListCustomers(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		opts := ListCustomersOptions{Search: r.URL.Query().Get("q")}
		customers, err := ListCustomers(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeCRMError(w, err)
			return
		}

		resp := make([]customerResponse, 0, len(customers))
		for _, c := range customers {
			resp = append(resp, toCustomerResponse(c))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createCustomerRequest struct {
	Name         string         `json:"name"`
	Email        string         `json:"email"`
	Phone        string         `json:"phone"`
	Address      string         `json:"address"`
	TaxID        string         `json:"taxId"`
	CreditLimit  string         `json:"creditLimit"`
	Currency     string         `json:"currency"`
	CustomFields map[string]any `json:"customFields"`
}

func handleCreateCustomer(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createCustomerRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		customer, err := CreateCustomer(r.Context(), pool, firmID, userID, CreateCustomerInput{
			Name: req.Name, Email: req.Email, Phone: req.Phone, Address: req.Address, TaxID: req.TaxID,
			CreditLimit: req.CreditLimit, Currency: req.Currency, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeCRMError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toCustomerResponse(customer))
	}
}

func handleGetCustomer(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		customerID, err := uuid.Parse(r.PathValue("customerID"))
		if err != nil {
			http.Error(w, "invalid customer id", http.StatusBadRequest)
			return
		}

		customer, err := GetCustomer(r.Context(), pool, firmID, userID, customerID)
		if err != nil {
			writeCRMError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toCustomerResponse(customer))
	}
}

type updateCustomerRequest struct {
	Name         *string        `json:"name"`
	Email        *string        `json:"email"`
	Phone        *string        `json:"phone"`
	Address      *string        `json:"address"`
	TaxID        *string        `json:"taxId"`
	CreditLimit  *string        `json:"creditLimit"`
	Currency     *string        `json:"currency"`
	CustomFields map[string]any `json:"customFields"`
}

func handleUpdateCustomer(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		customerID, err := uuid.Parse(r.PathValue("customerID"))
		if err != nil {
			http.Error(w, "invalid customer id", http.StatusBadRequest)
			return
		}
		var req updateCustomerRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		customer, err := UpdateCustomer(r.Context(), pool, firmID, userID, customerID, CustomerUpdate{
			Name: req.Name, Email: req.Email, Phone: req.Phone, Address: req.Address, TaxID: req.TaxID,
			CreditLimit: req.CreditLimit, Currency: req.Currency, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeCRMError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toCustomerResponse(customer))
	}
}

// ---------------------------------------------------------------------
// Interactions
// ---------------------------------------------------------------------

type interactionResponse struct {
	ID             string  `json:"id"`
	CustomerID     string  `json:"customerId"`
	Type           string  `json:"type"`
	Date           string  `json:"date"`
	Summary        string  `json:"summary"`
	Outcome        *string `json:"outcome,omitempty"`
	NextAction     *string `json:"nextAction,omitempty"`
	NextActionDate *string `json:"nextActionDate,omitempty"`
	OpportunityID  *string `json:"opportunityId,omitempty"`
	CreatedBy      string  `json:"createdBy"`
	CreatedAt      string  `json:"createdAt"`
}

func toInteractionResponse(i Interaction) interactionResponse {
	var opportunityID *string
	if i.OpportunityID != nil {
		s := i.OpportunityID.String()
		opportunityID = &s
	}
	var nextActionDate *string
	if i.NextActionDate != nil {
		s := i.NextActionDate.Format("2006-01-02")
		nextActionDate = &s
	}
	return interactionResponse{
		ID: i.ID.String(), CustomerID: i.CustomerID.String(), Type: i.Type, Date: i.Date.Format(time.RFC3339),
		Summary: i.Summary, Outcome: i.Outcome, NextAction: i.NextAction, NextActionDate: nextActionDate,
		OpportunityID: opportunityID, CreatedBy: i.CreatedBy.String(), CreatedAt: i.CreatedAt.Format(time.RFC3339),
	}
}

func handleListInteractions(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		customerID, err := uuid.Parse(r.PathValue("customerID"))
		if err != nil {
			http.Error(w, "invalid customer id", http.StatusBadRequest)
			return
		}
		interactions, err := ListInteractions(r.Context(), pool, firmID, userID, customerID)
		if err != nil {
			writeCRMError(w, err)
			return
		}
		resp := make([]interactionResponse, 0, len(interactions))
		for _, i := range interactions {
			resp = append(resp, toInteractionResponse(i))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createInteractionRequest struct {
	Type           string     `json:"type"`
	Date           time.Time  `json:"date"`
	Summary        string     `json:"summary"`
	Outcome        string     `json:"outcome"`
	NextAction     string     `json:"nextAction"`
	NextActionDate string     `json:"nextActionDate"`
	OpportunityID  *uuid.UUID `json:"opportunityId"`
}

func handleCreateInteraction(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		customerID, err := uuid.Parse(r.PathValue("customerID"))
		if err != nil {
			http.Error(w, "invalid customer id", http.StatusBadRequest)
			return
		}
		var req createInteractionRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		interaction, err := CreateInteraction(r.Context(), pool, firmID, userID, CreateInteractionInput{
			CustomerID: customerID, Type: req.Type, Date: req.Date, Summary: req.Summary, Outcome: req.Outcome,
			NextAction: req.NextAction, NextActionDate: req.NextActionDate, OpportunityID: req.OpportunityID,
		})
		if err != nil {
			writeCRMError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toInteractionResponse(interaction))
	}
}

// ---------------------------------------------------------------------
// Opportunities
// ---------------------------------------------------------------------

type opportunityResponse struct {
	ID                 string  `json:"id"`
	CustomerID         string  `json:"customerId"`
	Name               string  `json:"name"`
	Value              *string `json:"value,omitempty"`
	Currency           string  `json:"currency"`
	Stage              string  `json:"stage"`
	Probability        *int    `json:"probability,omitempty"`
	ExpectedCloseDate  *string `json:"expectedCloseDate,omitempty"`
	AssignedToPersonID *string `json:"assignedToPersonId,omitempty"`
	Notes              *string `json:"notes,omitempty"`
	CreatedAt          string  `json:"createdAt"`
	UpdatedAt          string  `json:"updatedAt"`
}

func toOpportunityResponse(o Opportunity) opportunityResponse {
	var assignedTo *string
	if o.AssignedToPersonID != nil {
		s := o.AssignedToPersonID.String()
		assignedTo = &s
	}
	var expectedClose *string
	if o.ExpectedCloseDate != nil {
		s := o.ExpectedCloseDate.Format("2006-01-02")
		expectedClose = &s
	}
	return opportunityResponse{
		ID: o.ID.String(), CustomerID: o.CustomerID.String(), Name: o.Name, Value: o.Value, Currency: o.Currency,
		Stage: string(o.Stage), Probability: o.Probability, ExpectedCloseDate: expectedClose,
		AssignedToPersonID: assignedTo, Notes: o.Notes,
		CreatedAt: o.CreatedAt.Format(time.RFC3339), UpdatedAt: o.UpdatedAt.Format(time.RFC3339),
	}
}

func handleListOpportunities(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var customerID *uuid.UUID
		if q := r.URL.Query().Get("customerId"); q != "" {
			id, err := uuid.Parse(q)
			if err != nil {
				http.Error(w, "invalid customerId", http.StatusBadRequest)
				return
			}
			customerID = &id
		}
		opportunities, err := ListOpportunities(r.Context(), pool, firmID, userID, customerID)
		if err != nil {
			writeCRMError(w, err)
			return
		}
		resp := make([]opportunityResponse, 0, len(opportunities))
		for _, o := range opportunities {
			resp = append(resp, toOpportunityResponse(o))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createOpportunityRequest struct {
	CustomerID         uuid.UUID  `json:"customerId"`
	Name               string     `json:"name"`
	Value              string     `json:"value"`
	Currency           string     `json:"currency"`
	Probability        *int       `json:"probability"`
	ExpectedCloseDate  string     `json:"expectedCloseDate"`
	AssignedToPersonID *uuid.UUID `json:"assignedToPersonId"`
	Notes              string     `json:"notes"`
}

func handleCreateOpportunity(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createOpportunityRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		opportunity, err := CreateOpportunity(r.Context(), pool, firmID, userID, CreateOpportunityInput{
			CustomerID: req.CustomerID, Name: req.Name, Value: req.Value, Currency: req.Currency,
			Probability: req.Probability, ExpectedCloseDate: req.ExpectedCloseDate,
			AssignedToPersonID: req.AssignedToPersonID, Notes: req.Notes,
		})
		if err != nil {
			writeCRMError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toOpportunityResponse(opportunity))
	}
}

func handleGetOpportunity(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		opportunityID, err := uuid.Parse(r.PathValue("opportunityID"))
		if err != nil {
			http.Error(w, "invalid opportunity id", http.StatusBadRequest)
			return
		}
		opportunity, err := GetOpportunity(r.Context(), pool, firmID, userID, opportunityID)
		if err != nil {
			writeCRMError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toOpportunityResponse(opportunity))
	}
}

type updateOpportunityRequest struct {
	Name               *string    `json:"name"`
	Value              *string    `json:"value"`
	Probability        *int       `json:"probability"`
	ExpectedCloseDate  *string    `json:"expectedCloseDate"`
	AssignedToPersonID *uuid.UUID `json:"assignedToPersonId"`
	Notes              *string    `json:"notes"`
}

func handleUpdateOpportunity(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		opportunityID, err := uuid.Parse(r.PathValue("opportunityID"))
		if err != nil {
			http.Error(w, "invalid opportunity id", http.StatusBadRequest)
			return
		}
		var req updateOpportunityRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		opportunity, err := UpdateOpportunity(r.Context(), pool, firmID, userID, opportunityID, UpdateOpportunityInput{
			Name: req.Name, Value: req.Value, Probability: req.Probability,
			ExpectedCloseDate: req.ExpectedCloseDate, AssignedToPersonID: req.AssignedToPersonID, Notes: req.Notes,
		})
		if err != nil {
			writeCRMError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toOpportunityResponse(opportunity))
	}
}

type updateOpportunityStageRequest struct {
	Stage string `json:"stage"`
}

func handleUpdateOpportunityStage(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		opportunityID, err := uuid.Parse(r.PathValue("opportunityID"))
		if err != nil {
			http.Error(w, "invalid opportunity id", http.StatusBadRequest)
			return
		}
		var req updateOpportunityStageRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		opportunity, err := UpdateOpportunityStage(r.Context(), pool, firmID, userID, opportunityID, OpportunityStage(req.Stage))
		if err != nil {
			writeCRMError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toOpportunityResponse(opportunity))
	}
}

// ---------------------------------------------------------------------
// Timeline
// ---------------------------------------------------------------------

type timelineEntryResponse struct {
	Kind        string               `json:"kind"`
	Date        string               `json:"date"`
	Interaction *interactionResponse `json:"interaction,omitempty"`
	Opportunity *opportunityResponse `json:"opportunity,omitempty"`
}

func handleGetCustomerTimeline(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		customerID, err := uuid.Parse(r.PathValue("customerID"))
		if err != nil {
			http.Error(w, "invalid customer id", http.StatusBadRequest)
			return
		}
		entries, err := GetCustomerTimeline(r.Context(), pool, firmID, userID, customerID)
		if err != nil {
			writeCRMError(w, err)
			return
		}
		resp := make([]timelineEntryResponse, 0, len(entries))
		for _, e := range entries {
			te := timelineEntryResponse{Kind: string(e.Kind), Date: e.Date.Format(time.RFC3339)}
			if e.Interaction != nil {
				ir := toInteractionResponse(*e.Interaction)
				te.Interaction = &ir
			}
			if e.Opportunity != nil {
				or := toOpportunityResponse(*e.Opportunity)
				te.Opportunity = &or
			}
			resp = append(resp, te)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
