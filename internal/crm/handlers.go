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
}

// writeCRMError maps this package's sentinel errors to the HTTP status
// that reflects why the caller isn't getting what they asked for - same
// convention as internal/inventory's writeInventoryError.
func writeCRMError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrCustomerNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidCustomer):
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
