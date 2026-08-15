// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package salesorders

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

// decodeJSONBody mirrors every other package's own copy (see
// internal/invoicing's identical helper).
func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires salesorders' HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention
// every other firm-scoped route group uses.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/sales-orders", auth(http.HandlerFunc(handleListSalesOrders(pool))))
	mux.Handle("POST /api/firms/{firmID}/sales-orders", auth(http.HandlerFunc(handleCreateSalesOrder(pool))))
	mux.Handle("GET /api/firms/{firmID}/sales-orders/{orderID}", auth(http.HandlerFunc(handleGetSalesOrder(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/sales-orders/{orderID}", auth(http.HandlerFunc(handleUpdateSalesOrderStatus(pool))))
}

// writeSalesOrderError maps this package's sentinel errors to the HTTP
// status that reflects why the caller isn't getting what they asked for -
// same convention as internal/invoicing's writeInvoicingError.
func writeSalesOrderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrSalesOrderNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidSalesOrder), errors.Is(err, ErrInvalidSalesOrderLine):
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

type salesOrderLineResponse struct {
	ID          string  `json:"id"`
	ProductID   *string `json:"productId,omitempty"`
	Description string  `json:"description"`
	Quantity    string  `json:"quantity"`
	UnitPrice   string  `json:"unitPrice"`
	TaxRate     string  `json:"taxRate"`
	LineTotal   string  `json:"lineTotal"`
}

func toLineResponse(l SalesOrderLine) salesOrderLineResponse {
	var productID *string
	if l.ProductID != nil {
		s := l.ProductID.String()
		productID = &s
	}
	return salesOrderLineResponse{
		ID: l.ID.String(), ProductID: productID, Description: l.Description,
		Quantity: l.Quantity, UnitPrice: l.UnitPrice, TaxRate: l.TaxRate, LineTotal: l.LineTotal,
	}
}

type salesOrderResponse struct {
	ID                     string                   `json:"id"`
	OrderNumber            string                   `json:"orderNumber"`
	CustomerID             *string                  `json:"customerId,omitempty"`
	Status                 string                   `json:"status"`
	ShippingAddress        *string                  `json:"shippingAddress,omitempty"`
	Notes                  *string                  `json:"notes,omitempty"`
	Currency               string                   `json:"currency"`
	Subtotal               string                   `json:"subtotal"`
	TaxAmount              string                   `json:"taxAmount"`
	Total                  string                   `json:"total"`
	SourceWorkflowInstance *string                  `json:"sourceWorkflowInstance,omitempty"`
	CreatedAt              string                   `json:"createdAt"`
	Lines                  []salesOrderLineResponse `json:"lines,omitempty"`
}

func toSalesOrderResponse(so SalesOrder) salesOrderResponse {
	var customerID *string
	if so.CustomerID != nil {
		s := so.CustomerID.String()
		customerID = &s
	}
	var sourceInstance *string
	if so.SourceWorkflowInstance != nil {
		s := so.SourceWorkflowInstance.String()
		sourceInstance = &s
	}
	lines := make([]salesOrderLineResponse, 0, len(so.Lines))
	for _, l := range so.Lines {
		lines = append(lines, toLineResponse(l))
	}
	return salesOrderResponse{
		ID: so.ID.String(), OrderNumber: so.OrderNumber, CustomerID: customerID, Status: string(so.Status),
		ShippingAddress: so.ShippingAddress, Notes: so.Notes, Currency: so.Currency,
		Subtotal: so.Subtotal, TaxAmount: so.TaxAmount, Total: so.Total,
		SourceWorkflowInstance: sourceInstance, CreatedAt: so.CreatedAt.Format(time.RFC3339), Lines: lines,
	}
}

func handleListSalesOrders(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		opts := ListOptions{Status: SalesOrderStatus(r.URL.Query().Get("status"))}
		orders, err := ListSalesOrders(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeSalesOrderError(w, err)
			return
		}

		resp := make([]salesOrderResponse, 0, len(orders))
		for _, so := range orders {
			resp = append(resp, toSalesOrderResponse(so))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createSalesOrderLineRequest struct {
	ProductID   *string `json:"productId"`
	Description string  `json:"description"`
	Quantity    string  `json:"quantity"`
	UnitPrice   string  `json:"unitPrice"`
	TaxRate     string  `json:"taxRate"`
}

func toLineInput(req createSalesOrderLineRequest) (SalesOrderLineInput, error) {
	var productID *uuid.UUID
	if req.ProductID != nil && *req.ProductID != "" {
		id, err := uuid.Parse(*req.ProductID)
		if err != nil {
			return SalesOrderLineInput{}, err
		}
		productID = &id
	}
	return SalesOrderLineInput{
		ProductID: productID, Description: req.Description, Quantity: req.Quantity, UnitPrice: req.UnitPrice, TaxRate: req.TaxRate,
	}, nil
}

type createSalesOrderRequest struct {
	CustomerID      string                        `json:"customerId"`
	ShippingAddress string                        `json:"shippingAddress"`
	Notes           string                        `json:"notes"`
	Currency        string                        `json:"currency"`
	Lines           []createSalesOrderLineRequest `json:"lines"`
}

func handleCreateSalesOrder(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createSalesOrderRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		var customerID *uuid.UUID
		if req.CustomerID != "" {
			id, err := uuid.Parse(req.CustomerID)
			if err != nil {
				http.Error(w, "invalid customer id", http.StatusBadRequest)
				return
			}
			customerID = &id
		}

		lines := make([]SalesOrderLineInput, 0, len(req.Lines))
		for _, l := range req.Lines {
			li, err := toLineInput(l)
			if err != nil {
				http.Error(w, "invalid line product id", http.StatusBadRequest)
				return
			}
			lines = append(lines, li)
		}

		so, err := CreateSalesOrder(r.Context(), pool, firmID, userID, customerID, req.ShippingAddress, req.Notes, req.Currency, lines)
		if err != nil {
			writeSalesOrderError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toSalesOrderResponse(so))
	}
}

func handleGetSalesOrder(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		orderID, err := uuid.Parse(r.PathValue("orderID"))
		if err != nil {
			http.Error(w, "invalid sales order id", http.StatusBadRequest)
			return
		}

		so, err := GetSalesOrder(r.Context(), pool, firmID, userID, orderID)
		if err != nil {
			writeSalesOrderError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toSalesOrderResponse(so))
	}
}

type updateSalesOrderStatusRequest struct {
	Status string `json:"status"`
}

func handleUpdateSalesOrderStatus(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		orderID, err := uuid.Parse(r.PathValue("orderID"))
		if err != nil {
			http.Error(w, "invalid sales order id", http.StatusBadRequest)
			return
		}
		var req updateSalesOrderStatusRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		so, err := UpdateSalesOrderStatus(r.Context(), pool, firmID, userID, orderID, SalesOrderStatus(req.Status))
		if err != nil {
			writeSalesOrderError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toSalesOrderResponse(so))
	}
}
