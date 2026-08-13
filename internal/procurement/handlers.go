// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package procurement

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

func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires procurement's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention
// every other firm-scoped route group uses.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/purchase-orders", auth(http.HandlerFunc(handleListPurchaseOrders(pool))))
	mux.Handle("POST /api/firms/{firmID}/purchase-orders", auth(http.HandlerFunc(handleCreatePurchaseOrder(pool))))
	mux.Handle("GET /api/firms/{firmID}/purchase-orders/{orderID}", auth(http.HandlerFunc(handleGetPurchaseOrder(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/purchase-orders/{orderID}", auth(http.HandlerFunc(handleUpdatePurchaseOrderStatus(pool))))
}

func writePurchaseOrderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrPurchaseOrderNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidPurchaseOrder), errors.Is(err, ErrInvalidPurchaseOrderLine):
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

type purchaseOrderLineResponse struct {
	ID          string  `json:"id"`
	ProductID   *string `json:"productId,omitempty"`
	Description string  `json:"description"`
	Quantity    string  `json:"quantity"`
	UnitPrice   string  `json:"unitPrice"`
	TaxRate     string  `json:"taxRate"`
	LineTotal   string  `json:"lineTotal"`
}

func toLineResponse(l PurchaseOrderLine) purchaseOrderLineResponse {
	var productID *string
	if l.ProductID != nil {
		s := l.ProductID.String()
		productID = &s
	}
	return purchaseOrderLineResponse{
		ID: l.ID.String(), ProductID: productID, Description: l.Description,
		Quantity: l.Quantity, UnitPrice: l.UnitPrice, TaxRate: l.TaxRate, LineTotal: l.LineTotal,
	}
}

type purchaseOrderResponse struct {
	ID                     string                      `json:"id"`
	OrderNumber            string                      `json:"orderNumber"`
	SupplierID             *string                     `json:"supplierId,omitempty"`
	Status                 string                      `json:"status"`
	Notes                  *string                     `json:"notes,omitempty"`
	Currency               string                      `json:"currency"`
	Subtotal               string                      `json:"subtotal"`
	TaxAmount              string                      `json:"taxAmount"`
	Total                  string                      `json:"total"`
	SourceWorkflowInstance *string                     `json:"sourceWorkflowInstance,omitempty"`
	CreatedAt              string                      `json:"createdAt"`
	Lines                  []purchaseOrderLineResponse `json:"lines,omitempty"`
}

func toPurchaseOrderResponse(po PurchaseOrder) purchaseOrderResponse {
	var supplierID *string
	if po.SupplierID != nil {
		s := po.SupplierID.String()
		supplierID = &s
	}
	var sourceInstance *string
	if po.SourceWorkflowInstance != nil {
		s := po.SourceWorkflowInstance.String()
		sourceInstance = &s
	}
	lines := make([]purchaseOrderLineResponse, 0, len(po.Lines))
	for _, l := range po.Lines {
		lines = append(lines, toLineResponse(l))
	}
	return purchaseOrderResponse{
		ID: po.ID.String(), OrderNumber: po.OrderNumber, SupplierID: supplierID, Status: string(po.Status),
		Notes: po.Notes, Currency: po.Currency, Subtotal: po.Subtotal, TaxAmount: po.TaxAmount, Total: po.Total,
		SourceWorkflowInstance: sourceInstance, CreatedAt: po.CreatedAt.Format(time.RFC3339), Lines: lines,
	}
}

func handleListPurchaseOrders(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		opts := ListOptions{Status: PurchaseOrderStatus(r.URL.Query().Get("status"))}
		orders, err := ListPurchaseOrders(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writePurchaseOrderError(w, err)
			return
		}

		resp := make([]purchaseOrderResponse, 0, len(orders))
		for _, po := range orders {
			resp = append(resp, toPurchaseOrderResponse(po))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createPurchaseOrderLineRequest struct {
	ProductID   *string `json:"productId"`
	Description string  `json:"description"`
	Quantity    string  `json:"quantity"`
	UnitPrice   string  `json:"unitPrice"`
	TaxRate     string  `json:"taxRate"`
}

func toLineInput(req createPurchaseOrderLineRequest) (PurchaseOrderLineInput, error) {
	var productID *uuid.UUID
	if req.ProductID != nil && *req.ProductID != "" {
		id, err := uuid.Parse(*req.ProductID)
		if err != nil {
			return PurchaseOrderLineInput{}, err
		}
		productID = &id
	}
	return PurchaseOrderLineInput{
		ProductID: productID, Description: req.Description, Quantity: req.Quantity, UnitPrice: req.UnitPrice, TaxRate: req.TaxRate,
	}, nil
}

type createPurchaseOrderRequest struct {
	SupplierID string                           `json:"supplierId"`
	Notes      string                           `json:"notes"`
	Currency   string                           `json:"currency"`
	Lines      []createPurchaseOrderLineRequest `json:"lines"`
}

func handleCreatePurchaseOrder(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createPurchaseOrderRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		var supplierID *uuid.UUID
		if req.SupplierID != "" {
			id, err := uuid.Parse(req.SupplierID)
			if err != nil {
				http.Error(w, "invalid supplier id", http.StatusBadRequest)
				return
			}
			supplierID = &id
		}

		lines := make([]PurchaseOrderLineInput, 0, len(req.Lines))
		for _, l := range req.Lines {
			li, err := toLineInput(l)
			if err != nil {
				http.Error(w, "invalid line product id", http.StatusBadRequest)
				return
			}
			lines = append(lines, li)
		}

		po, err := CreatePurchaseOrder(r.Context(), pool, firmID, userID, supplierID, req.Notes, req.Currency, lines)
		if err != nil {
			writePurchaseOrderError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toPurchaseOrderResponse(po))
	}
}

func handleGetPurchaseOrder(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		orderID, err := uuid.Parse(r.PathValue("orderID"))
		if err != nil {
			http.Error(w, "invalid purchase order id", http.StatusBadRequest)
			return
		}

		po, err := GetPurchaseOrder(r.Context(), pool, firmID, userID, orderID)
		if err != nil {
			writePurchaseOrderError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPurchaseOrderResponse(po))
	}
}

type updatePurchaseOrderStatusRequest struct {
	Status string `json:"status"`
}

func handleUpdatePurchaseOrderStatus(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		orderID, err := uuid.Parse(r.PathValue("orderID"))
		if err != nil {
			http.Error(w, "invalid purchase order id", http.StatusBadRequest)
			return
		}
		var req updatePurchaseOrderStatusRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		po, err := UpdatePurchaseOrderStatus(r.Context(), pool, firmID, userID, orderID, PurchaseOrderStatus(req.Status))
		if err != nil {
			writePurchaseOrderError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPurchaseOrderResponse(po))
	}
}
