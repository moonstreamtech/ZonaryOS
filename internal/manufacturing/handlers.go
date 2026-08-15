// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package manufacturing

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

const dateLayout = "2006-01-02"

func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires manufacturing's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention
// every other firm-scoped route group uses.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/boms", auth(http.HandlerFunc(handleListBOMs(pool))))
	mux.Handle("POST /api/firms/{firmID}/boms", auth(http.HandlerFunc(handleCreateBOM(pool))))
	mux.Handle("GET /api/firms/{firmID}/boms/{bomID}", auth(http.HandlerFunc(handleGetBOM(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/boms/{bomID}", auth(http.HandlerFunc(handleUpdateBOM(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/boms/{bomID}", auth(http.HandlerFunc(handleDeleteBOM(pool))))
	mux.Handle("POST /api/firms/{firmID}/boms/{bomID}/lines", auth(http.HandlerFunc(handleAddBOMLine(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/boms/{bomID}/lines/{lineID}", auth(http.HandlerFunc(handleDeleteBOMLine(pool))))

	mux.Handle("GET /api/firms/{firmID}/production-orders", auth(http.HandlerFunc(handleListProductionOrders(pool))))
	mux.Handle("POST /api/firms/{firmID}/production-orders", auth(http.HandlerFunc(handleCreateProductionOrder(pool))))
	mux.Handle("GET /api/firms/{firmID}/production-orders/{orderID}", auth(http.HandlerFunc(handleGetProductionOrder(pool))))
	mux.Handle("POST /api/firms/{firmID}/production-orders/{orderID}/start", auth(http.HandlerFunc(handleStartProductionOrder(pool))))
	mux.Handle("POST /api/firms/{firmID}/production-orders/{orderID}/complete", auth(http.HandlerFunc(handleCompleteProductionOrder(pool))))
	mux.Handle("POST /api/firms/{firmID}/production-orders/{orderID}/cancel", auth(http.HandlerFunc(handleCancelProductionOrder(pool))))
}

// writeManufacturingError maps this package's sentinel errors to the HTTP
// status that reflects why the caller isn't getting what they asked for -
// same convention as internal/invoicing's writeInvoicingError.
func writeManufacturingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrBOMNotFound), errors.Is(err, ErrBOMLineNotFound), errors.Is(err, ErrProductionOrderNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidBOM), errors.Is(err, ErrInvalidBOMLine), errors.Is(err, ErrInvalidProductionOrder), errors.Is(err, ErrInvalidProductionOrderTransition):
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

type bomLineResponse struct {
	ID                 string  `json:"id"`
	ComponentProductID string  `json:"componentProductId"`
	QuantityPerUnit    string  `json:"quantityPerUnit"`
	Unit               string  `json:"unit"`
	Notes              *string `json:"notes,omitempty"`
}

func toBOMLineResponse(l BOMLine) bomLineResponse {
	return bomLineResponse{
		ID: l.ID.String(), ComponentProductID: l.ComponentProductID.String(),
		QuantityPerUnit: l.QuantityPerUnit, Unit: l.Unit, Notes: l.Notes,
	}
}

type bomResponse struct {
	ID        string            `json:"id"`
	ProductID string            `json:"productId"`
	Name      string            `json:"name"`
	Version   string            `json:"version"`
	IsActive  bool              `json:"isActive"`
	UnitYield string            `json:"unitYield"`
	Notes     *string           `json:"notes,omitempty"`
	CreatedAt string            `json:"createdAt"`
	Lines     []bomLineResponse `json:"lines,omitempty"`
}

func toBOMResponse(b BOMHeader) bomResponse {
	lines := make([]bomLineResponse, 0, len(b.Lines))
	for _, l := range b.Lines {
		lines = append(lines, toBOMLineResponse(l))
	}
	return bomResponse{
		ID: b.ID.String(), ProductID: b.ProductID.String(), Name: b.Name, Version: b.Version,
		IsActive: b.IsActive, UnitYield: b.UnitYield, Notes: b.Notes, CreatedAt: b.CreatedAt.Format(time.RFC3339), Lines: lines,
	}
}

func handleListBOMs(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var productID *uuid.UUID
		if raw := r.URL.Query().Get("productId"); raw != "" {
			id, err := uuid.Parse(raw)
			if err != nil {
				http.Error(w, "invalid productId", http.StatusBadRequest)
				return
			}
			productID = &id
		}

		boms, err := ListBOMs(r.Context(), pool, firmID, userID, productID)
		if err != nil {
			writeManufacturingError(w, err)
			return
		}

		resp := make([]bomResponse, 0, len(boms))
		for _, b := range boms {
			resp = append(resp, toBOMResponse(b))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createBOMLineRequest struct {
	ComponentProductID string `json:"componentProductId"`
	QuantityPerUnit    string `json:"quantityPerUnit"`
	Unit               string `json:"unit"`
	Notes              string `json:"notes"`
}

func toLineInput(req createBOMLineRequest) (BOMLineInput, error) {
	id, err := uuid.Parse(req.ComponentProductID)
	if err != nil {
		return BOMLineInput{}, err
	}
	return BOMLineInput{ComponentProductID: id, QuantityPerUnit: req.QuantityPerUnit, Unit: req.Unit, Notes: req.Notes}, nil
}

type createBOMRequest struct {
	ProductID string                 `json:"productId"`
	Name      string                 `json:"name"`
	Version   string                 `json:"version"`
	IsActive  *bool                  `json:"isActive"`
	UnitYield string                 `json:"unitYield"`
	Notes     string                 `json:"notes"`
	Lines     []createBOMLineRequest `json:"lines"`
}

func handleCreateBOM(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createBOMRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		productID, err := uuid.Parse(req.ProductID)
		if err != nil {
			http.Error(w, "invalid product id", http.StatusBadRequest)
			return
		}
		lines := make([]BOMLineInput, 0, len(req.Lines))
		for _, l := range req.Lines {
			li, err := toLineInput(l)
			if err != nil {
				http.Error(w, "invalid line component product id", http.StatusBadRequest)
				return
			}
			lines = append(lines, li)
		}

		bom, err := CreateBOM(r.Context(), pool, firmID, userID, CreateBOMInput{
			ProductID: productID, Name: req.Name, Version: req.Version, IsActive: req.IsActive,
			UnitYield: req.UnitYield, Notes: req.Notes, Lines: lines,
		})
		if err != nil {
			writeManufacturingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toBOMResponse(bom))
	}
}

func handleGetBOM(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		bomID, err := uuid.Parse(r.PathValue("bomID"))
		if err != nil {
			http.Error(w, "invalid bom id", http.StatusBadRequest)
			return
		}

		bom, err := GetBOM(r.Context(), pool, firmID, userID, bomID)
		if err != nil {
			writeManufacturingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toBOMResponse(bom))
	}
}

type updateBOMRequest struct {
	Name     string  `json:"name"`
	Notes    *string `json:"notes"`
	IsActive *bool   `json:"isActive"`
}

func handleUpdateBOM(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		bomID, err := uuid.Parse(r.PathValue("bomID"))
		if err != nil {
			http.Error(w, "invalid bom id", http.StatusBadRequest)
			return
		}
		var req updateBOMRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		bom, err := UpdateBOM(r.Context(), pool, firmID, userID, bomID, UpdateBOMInput{Name: req.Name, Notes: req.Notes, IsActive: req.IsActive})
		if err != nil {
			writeManufacturingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toBOMResponse(bom))
	}
}

func handleDeleteBOM(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		bomID, err := uuid.Parse(r.PathValue("bomID"))
		if err != nil {
			http.Error(w, "invalid bom id", http.StatusBadRequest)
			return
		}

		if err := DeleteBOM(r.Context(), pool, firmID, userID, bomID); err != nil {
			writeManufacturingError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleAddBOMLine(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		bomID, err := uuid.Parse(r.PathValue("bomID"))
		if err != nil {
			http.Error(w, "invalid bom id", http.StatusBadRequest)
			return
		}
		var req createBOMLineRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		li, err := toLineInput(req)
		if err != nil {
			http.Error(w, "invalid component product id", http.StatusBadRequest)
			return
		}

		bom, err := AddBOMLine(r.Context(), pool, firmID, userID, bomID, li)
		if err != nil {
			writeManufacturingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toBOMResponse(bom))
	}
}

func handleDeleteBOMLine(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		bomID, err := uuid.Parse(r.PathValue("bomID"))
		if err != nil {
			http.Error(w, "invalid bom id", http.StatusBadRequest)
			return
		}
		lineID, err := uuid.Parse(r.PathValue("lineID"))
		if err != nil {
			http.Error(w, "invalid line id", http.StatusBadRequest)
			return
		}

		bom, err := DeleteBOMLine(r.Context(), pool, firmID, userID, bomID, lineID)
		if err != nil {
			writeManufacturingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toBOMResponse(bom))
	}
}

type materialIssueResponse struct {
	ID             string `json:"id"`
	ProductID      string `json:"productId"`
	QuantityIssued string `json:"quantityIssued"`
	IssuedAt       string `json:"issuedAt"`
}

func toMaterialIssueResponse(m MaterialIssue) materialIssueResponse {
	return materialIssueResponse{
		ID: m.ID.String(), ProductID: m.ProductID.String(), QuantityIssued: m.QuantityIssued, IssuedAt: m.IssuedAt.Format(time.RFC3339),
	}
}

type productionOrderResponse struct {
	ID               string                  `json:"id"`
	OrderNumber      string                  `json:"orderNumber"`
	ProductID        string                  `json:"productId"`
	BOMID            string                  `json:"bomId"`
	QuantityPlanned  string                  `json:"quantityPlanned"`
	QuantityProduced string                  `json:"quantityProduced"`
	Status           string                  `json:"status"`
	PlannedStart     *string                 `json:"plannedStart,omitempty"`
	PlannedEnd       *string                 `json:"plannedEnd,omitempty"`
	ActualStart      *string                 `json:"actualStart,omitempty"`
	ActualEnd        *string                 `json:"actualEnd,omitempty"`
	Notes            *string                 `json:"notes,omitempty"`
	CreatedAt        string                  `json:"createdAt"`
	MaterialIssues   []materialIssueResponse `json:"materialIssues,omitempty"`
}

func formatDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(dateLayout)
	return &s
}

func formatTimestamp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

func toProductionOrderResponse(po ProductionOrder) productionOrderResponse {
	issues := make([]materialIssueResponse, 0, len(po.MaterialIssues))
	for _, m := range po.MaterialIssues {
		issues = append(issues, toMaterialIssueResponse(m))
	}
	return productionOrderResponse{
		ID: po.ID.String(), OrderNumber: po.OrderNumber, ProductID: po.ProductID.String(), BOMID: po.BOMID.String(),
		QuantityPlanned: po.QuantityPlanned, QuantityProduced: po.QuantityProduced, Status: string(po.Status),
		PlannedStart: formatDate(po.PlannedStart), PlannedEnd: formatDate(po.PlannedEnd),
		ActualStart: formatTimestamp(po.ActualStart), ActualEnd: formatTimestamp(po.ActualEnd),
		Notes: po.Notes, CreatedAt: po.CreatedAt.Format(time.RFC3339), MaterialIssues: issues,
	}
}

func handleListProductionOrders(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		opts := ListOptions{Status: ProductionOrderStatus(r.URL.Query().Get("status"))}
		orders, err := ListProductionOrders(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeManufacturingError(w, err)
			return
		}

		resp := make([]productionOrderResponse, 0, len(orders))
		for _, po := range orders {
			resp = append(resp, toProductionOrderResponse(po))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func parseOptionalDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

type createProductionOrderRequest struct {
	ProductID       string `json:"productId"`
	BOMID           string `json:"bomId"`
	QuantityPlanned string `json:"quantityPlanned"`
	PlannedStart    string `json:"plannedStart"`
	PlannedEnd      string `json:"plannedEnd"`
	Notes           string `json:"notes"`
}

func handleCreateProductionOrder(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createProductionOrderRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		productID, err := uuid.Parse(req.ProductID)
		if err != nil {
			http.Error(w, "invalid product id", http.StatusBadRequest)
			return
		}
		bomID, err := uuid.Parse(req.BOMID)
		if err != nil {
			http.Error(w, "invalid bom id", http.StatusBadRequest)
			return
		}
		plannedStart, err := parseOptionalDate(req.PlannedStart)
		if err != nil {
			http.Error(w, "invalid plannedStart", http.StatusBadRequest)
			return
		}
		plannedEnd, err := parseOptionalDate(req.PlannedEnd)
		if err != nil {
			http.Error(w, "invalid plannedEnd", http.StatusBadRequest)
			return
		}

		po, err := CreateProductionOrder(r.Context(), pool, firmID, userID, CreateProductionOrderInput{
			ProductID: productID, BOMID: bomID, QuantityPlanned: req.QuantityPlanned,
			PlannedStart: plannedStart, PlannedEnd: plannedEnd, Notes: req.Notes,
		})
		if err != nil {
			writeManufacturingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toProductionOrderResponse(po))
	}
}

func handleGetProductionOrder(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		orderID, err := uuid.Parse(r.PathValue("orderID"))
		if err != nil {
			http.Error(w, "invalid production order id", http.StatusBadRequest)
			return
		}

		po, err := GetProductionOrder(r.Context(), pool, firmID, userID, orderID)
		if err != nil {
			writeManufacturingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toProductionOrderResponse(po))
	}
}

func handleStartProductionOrder(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		orderID, err := uuid.Parse(r.PathValue("orderID"))
		if err != nil {
			http.Error(w, "invalid production order id", http.StatusBadRequest)
			return
		}

		po, err := StartProductionOrder(r.Context(), pool, firmID, userID, orderID)
		if err != nil {
			writeManufacturingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toProductionOrderResponse(po))
	}
}

type completeProductionOrderRequest struct {
	QuantityProduced string `json:"quantityProduced"`
}

func handleCompleteProductionOrder(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		orderID, err := uuid.Parse(r.PathValue("orderID"))
		if err != nil {
			http.Error(w, "invalid production order id", http.StatusBadRequest)
			return
		}
		var req completeProductionOrderRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		po, err := CompleteProductionOrder(r.Context(), pool, firmID, userID, orderID, req.QuantityProduced)
		if err != nil {
			writeManufacturingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toProductionOrderResponse(po))
	}
}

func handleCancelProductionOrder(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		orderID, err := uuid.Parse(r.PathValue("orderID"))
		if err != nil {
			http.Error(w, "invalid production order id", http.StatusBadRequest)
			return
		}

		po, err := CancelProductionOrder(r.Context(), pool, firmID, userID, orderID)
		if err != nil {
			writeManufacturingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toProductionOrderResponse(po))
	}
}
