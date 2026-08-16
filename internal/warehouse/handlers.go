// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package warehouse

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

// RegisterRoutes wires internal/warehouse's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/warehouses", auth(http.HandlerFunc(handleListWarehouses(pool))))
	mux.Handle("POST /api/firms/{firmID}/warehouses", auth(http.HandlerFunc(handleCreateWarehouse(pool))))
	mux.Handle("GET /api/firms/{firmID}/warehouses/{warehouseID}", auth(http.HandlerFunc(handleGetWarehouse(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/warehouses/{warehouseID}", auth(http.HandlerFunc(handleUpdateWarehouse(pool))))
	mux.Handle("GET /api/firms/{firmID}/warehouses/{warehouseID}/stock", auth(http.HandlerFunc(handleGetWarehouseStock(pool))))
	mux.Handle("GET /api/firms/{firmID}/warehouses/{warehouseID}/locations", auth(http.HandlerFunc(handleListLocations(pool))))
	mux.Handle("POST /api/firms/{firmID}/warehouses/{warehouseID}/locations", auth(http.HandlerFunc(handleCreateLocation(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/locations/{locationID}", auth(http.HandlerFunc(handleUpdateLocation(pool))))

	mux.Handle("GET /api/firms/{firmID}/inventory-transfers", auth(http.HandlerFunc(handleListTransfers(pool))))
	mux.Handle("POST /api/firms/{firmID}/inventory-transfers", auth(http.HandlerFunc(handleCreateTransfer(pool))))
	mux.Handle("GET /api/firms/{firmID}/inventory-transfers/{transferID}", auth(http.HandlerFunc(handleGetTransfer(pool))))
	mux.Handle("POST /api/firms/{firmID}/inventory-transfers/{transferID}/complete", auth(http.HandlerFunc(handleCompleteTransfer(pool))))
	mux.Handle("POST /api/firms/{firmID}/inventory-transfers/{transferID}/cancel", auth(http.HandlerFunc(handleCancelTransfer(pool))))

	mux.Handle("GET /api/firms/{firmID}/cycle-counts", auth(http.HandlerFunc(handleListCycleCountSessions(pool))))
	mux.Handle("POST /api/firms/{firmID}/cycle-counts", auth(http.HandlerFunc(handleCreateCycleCountSession(pool))))
	mux.Handle("GET /api/firms/{firmID}/cycle-counts/{sessionID}", auth(http.HandlerFunc(handleGetCycleCountSession(pool))))
	mux.Handle("POST /api/firms/{firmID}/cycle-counts/{sessionID}/lines/{lineID}/count", auth(http.HandlerFunc(handleRecordCount(pool))))
	mux.Handle("POST /api/firms/{firmID}/cycle-counts/{sessionID}/complete", auth(http.HandlerFunc(handleCompleteCycleCountSession(pool))))
}

// writeWarehouseError maps this package's sentinel errors to the HTTP
// status that reflects why the caller isn't getting what they asked for -
// same convention as internal/manufacturing's writeManufacturingError.
func writeWarehouseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrWarehouseNotFound), errors.Is(err, ErrLocationNotFound),
		errors.Is(err, ErrTransferNotFound), errors.Is(err, ErrCycleCountSessionNotFound), errors.Is(err, ErrCycleCountLineNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidWarehouse), errors.Is(err, ErrInvalidLocation), errors.Is(err, ErrInvalidTransfer),
		errors.Is(err, ErrInvalidTransferTransition), errors.Is(err, ErrInvalidCycleCount), errors.Is(err, ErrInvalidCycleCountTransition):
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

// ---------------------------------------------------------------------
// Warehouses
// ---------------------------------------------------------------------

type warehouseResponse struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Address   *string `json:"address,omitempty"`
	IsActive  bool    `json:"isActive"`
	CreatedAt string  `json:"createdAt"`
}

func toWarehouseResponse(w Warehouse) warehouseResponse {
	return warehouseResponse{ID: w.ID.String(), Name: w.Name, Address: w.Address, IsActive: w.IsActive, CreatedAt: w.CreatedAt.Format(time.RFC3339)}
}

func handleListWarehouses(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		warehouses, err := ListWarehouses(r.Context(), pool, firmID, userID)
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		resp := make([]warehouseResponse, 0, len(warehouses))
		for _, wh := range warehouses {
			resp = append(resp, toWarehouseResponse(wh))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createWarehouseRequest struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

func handleCreateWarehouse(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createWarehouseRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		wh, err := CreateWarehouse(r.Context(), pool, firmID, userID, CreateWarehouseInput{Name: req.Name, Address: req.Address})
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toWarehouseResponse(wh))
	}
}

func handleGetWarehouse(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		warehouseID, err := uuid.Parse(r.PathValue("warehouseID"))
		if err != nil {
			http.Error(w, "invalid warehouse id", http.StatusBadRequest)
			return
		}
		wh, err := GetWarehouse(r.Context(), pool, firmID, userID, warehouseID)
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toWarehouseResponse(wh))
	}
}

type updateWarehouseRequest struct {
	Name     *string `json:"name"`
	Address  *string `json:"address"`
	IsActive *bool   `json:"isActive"`
}

func handleUpdateWarehouse(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		warehouseID, err := uuid.Parse(r.PathValue("warehouseID"))
		if err != nil {
			http.Error(w, "invalid warehouse id", http.StatusBadRequest)
			return
		}
		var req updateWarehouseRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		wh, err := UpdateWarehouse(r.Context(), pool, firmID, userID, warehouseID, UpdateWarehouseInput{Name: req.Name, Address: req.Address, IsActive: req.IsActive})
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toWarehouseResponse(wh))
	}
}

type locationStockResponse struct {
	LocationID   string `json:"locationId"`
	LocationCode string `json:"locationCode"`
	ProductID    string `json:"productId"`
	ProductSKU   string `json:"productSku"`
	Quantity     string `json:"quantity"`
}

func handleGetWarehouseStock(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		warehouseID, err := uuid.Parse(r.PathValue("warehouseID"))
		if err != nil {
			http.Error(w, "invalid warehouse id", http.StatusBadRequest)
			return
		}
		stock, err := GetWarehouseStock(r.Context(), pool, firmID, userID, warehouseID)
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		resp := make([]locationStockResponse, 0, len(stock))
		for _, s := range stock {
			resp = append(resp, locationStockResponse{
				LocationID: s.LocationID.String(), LocationCode: s.LocationCode,
				ProductID: s.ProductID.String(), ProductSKU: s.ProductSKU, Quantity: s.Quantity,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// ---------------------------------------------------------------------
// Locations
// ---------------------------------------------------------------------

type locationResponse struct {
	ID          string  `json:"id"`
	WarehouseID string  `json:"warehouseId"`
	Code        string  `json:"code"`
	Name        *string `json:"name,omitempty"`
	Type        string  `json:"type"`
	ParentID    *string `json:"parentId,omitempty"`
	IsDefault   bool    `json:"isDefault"`
	IsActive    bool    `json:"isActive"`
	CreatedAt   string  `json:"createdAt"`
}

func toLocationResponse(l WarehouseLocation) locationResponse {
	var parentID *string
	if l.ParentID != nil {
		s := l.ParentID.String()
		parentID = &s
	}
	return locationResponse{
		ID: l.ID.String(), WarehouseID: l.WarehouseID.String(), Code: l.Code, Name: l.Name, Type: l.Type,
		ParentID: parentID, IsDefault: l.IsDefault, IsActive: l.IsActive, CreatedAt: l.CreatedAt.Format(time.RFC3339),
	}
}

func handleListLocations(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		warehouseID, err := uuid.Parse(r.PathValue("warehouseID"))
		if err != nil {
			http.Error(w, "invalid warehouse id", http.StatusBadRequest)
			return
		}
		locations, err := ListLocations(r.Context(), pool, firmID, userID, warehouseID)
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		resp := make([]locationResponse, 0, len(locations))
		for _, l := range locations {
			resp = append(resp, toLocationResponse(l))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createLocationRequest struct {
	Code     string     `json:"code"`
	Name     string     `json:"name"`
	Type     string     `json:"type"`
	ParentID *uuid.UUID `json:"parentId"`
}

func handleCreateLocation(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		warehouseID, err := uuid.Parse(r.PathValue("warehouseID"))
		if err != nil {
			http.Error(w, "invalid warehouse id", http.StatusBadRequest)
			return
		}
		var req createLocationRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		loc, err := CreateLocation(r.Context(), pool, firmID, userID, warehouseID, CreateLocationInput{
			Code: req.Code, Name: req.Name, Type: req.Type, ParentID: req.ParentID,
		})
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toLocationResponse(loc))
	}
}

type updateLocationRequest struct {
	Name     *string `json:"name"`
	Type     *string `json:"type"`
	IsActive *bool   `json:"isActive"`
}

func handleUpdateLocation(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		locationID, err := uuid.Parse(r.PathValue("locationID"))
		if err != nil {
			http.Error(w, "invalid location id", http.StatusBadRequest)
			return
		}
		var req updateLocationRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		loc, err := UpdateLocation(r.Context(), pool, firmID, userID, locationID, UpdateLocationInput{Name: req.Name, Type: req.Type, IsActive: req.IsActive})
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toLocationResponse(loc))
	}
}

// ---------------------------------------------------------------------
// Inventory transfers
// ---------------------------------------------------------------------

type transferResponse struct {
	ID             string  `json:"id"`
	FromLocationID string  `json:"fromLocationId"`
	ToLocationID   string  `json:"toLocationId"`
	ProductID      string  `json:"productId"`
	Quantity       string  `json:"quantity"`
	Status         string  `json:"status"`
	Notes          *string `json:"notes,omitempty"`
	CreatedAt      string  `json:"createdAt"`
}

func toTransferResponse(t Transfer) transferResponse {
	return transferResponse{
		ID: t.ID.String(), FromLocationID: t.FromLocationID.String(), ToLocationID: t.ToLocationID.String(),
		ProductID: t.ProductID.String(), Quantity: t.Quantity, Status: string(t.Status), Notes: t.Notes,
		CreatedAt: t.CreatedAt.Format(time.RFC3339),
	}
}

func handleListTransfers(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		transfers, err := ListTransfers(r.Context(), pool, firmID, userID)
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		resp := make([]transferResponse, 0, len(transfers))
		for _, t := range transfers {
			resp = append(resp, toTransferResponse(t))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createTransferRequest struct {
	FromLocationID uuid.UUID `json:"fromLocationId"`
	ToLocationID   uuid.UUID `json:"toLocationId"`
	ProductID      uuid.UUID `json:"productId"`
	Quantity       string    `json:"quantity"`
	Notes          string    `json:"notes"`
}

func handleCreateTransfer(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createTransferRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		t, err := CreateTransfer(r.Context(), pool, firmID, userID, CreateTransferInput{
			FromLocationID: req.FromLocationID, ToLocationID: req.ToLocationID, ProductID: req.ProductID,
			Quantity: req.Quantity, Notes: req.Notes,
		})
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toTransferResponse(t))
	}
}

func handleGetTransfer(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		transferID, err := uuid.Parse(r.PathValue("transferID"))
		if err != nil {
			http.Error(w, "invalid transfer id", http.StatusBadRequest)
			return
		}
		t, err := GetTransfer(r.Context(), pool, firmID, userID, transferID)
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toTransferResponse(t))
	}
}

func handleCompleteTransfer(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		transferID, err := uuid.Parse(r.PathValue("transferID"))
		if err != nil {
			http.Error(w, "invalid transfer id", http.StatusBadRequest)
			return
		}
		t, err := CompleteTransfer(r.Context(), pool, firmID, userID, transferID)
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toTransferResponse(t))
	}
}

func handleCancelTransfer(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		transferID, err := uuid.Parse(r.PathValue("transferID"))
		if err != nil {
			http.Error(w, "invalid transfer id", http.StatusBadRequest)
			return
		}
		t, err := CancelTransfer(r.Context(), pool, firmID, userID, transferID)
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toTransferResponse(t))
	}
}

// ---------------------------------------------------------------------
// Cycle counts
// ---------------------------------------------------------------------

type cycleCountLineResponse struct {
	ID              string  `json:"id"`
	LocationID      string  `json:"locationId"`
	ProductID       string  `json:"productId"`
	SystemQuantity  string  `json:"systemQuantity"`
	CountedQuantity *string `json:"countedQuantity,omitempty"`
	Variance        *string `json:"variance,omitempty"`
	Adjusted        bool    `json:"adjusted"`
}

func toCycleCountLineResponse(l CycleCountLine) cycleCountLineResponse {
	return cycleCountLineResponse{
		ID: l.ID.String(), LocationID: l.LocationID.String(), ProductID: l.ProductID.String(),
		SystemQuantity: l.SystemQuantity, CountedQuantity: l.CountedQuantity, Variance: l.Variance, Adjusted: l.Adjusted,
	}
}

type cycleCountSessionResponse struct {
	ID          string                   `json:"id"`
	WarehouseID string                   `json:"warehouseId"`
	Status      string                   `json:"status"`
	CreatedAt   string                   `json:"createdAt"`
	Lines       []cycleCountLineResponse `json:"lines,omitempty"`
}

func toCycleCountSessionResponse(s CycleCountSession) cycleCountSessionResponse {
	lines := make([]cycleCountLineResponse, 0, len(s.Lines))
	for _, l := range s.Lines {
		lines = append(lines, toCycleCountLineResponse(l))
	}
	return cycleCountSessionResponse{
		ID: s.ID.String(), WarehouseID: s.WarehouseID.String(), Status: string(s.Status),
		CreatedAt: s.CreatedAt.Format(time.RFC3339), Lines: lines,
	}
}

func handleListCycleCountSessions(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		sessions, err := ListCycleCountSessions(r.Context(), pool, firmID, userID)
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		resp := make([]cycleCountSessionResponse, 0, len(sessions))
		for _, s := range sessions {
			resp = append(resp, toCycleCountSessionResponse(s))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createCycleCountSessionRequest struct {
	WarehouseID uuid.UUID `json:"warehouseId"`
	Lines       []struct {
		LocationID uuid.UUID `json:"locationId"`
		ProductID  uuid.UUID `json:"productId"`
	} `json:"lines"`
}

func handleCreateCycleCountSession(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createCycleCountSessionRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		lines := make([]CycleCountLineInput, 0, len(req.Lines))
		for _, l := range req.Lines {
			lines = append(lines, CycleCountLineInput{LocationID: l.LocationID, ProductID: l.ProductID})
		}
		s, err := CreateCycleCountSession(r.Context(), pool, firmID, userID, CreateCycleCountSessionInput{WarehouseID: req.WarehouseID, Lines: lines})
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toCycleCountSessionResponse(s))
	}
}

func handleGetCycleCountSession(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		sessionID, err := uuid.Parse(r.PathValue("sessionID"))
		if err != nil {
			http.Error(w, "invalid session id", http.StatusBadRequest)
			return
		}
		s, err := GetCycleCountSession(r.Context(), pool, firmID, userID, sessionID)
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toCycleCountSessionResponse(s))
	}
}

type recordCountRequest struct {
	CountedQuantity string `json:"countedQuantity"`
}

func handleRecordCount(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		sessionID, err := uuid.Parse(r.PathValue("sessionID"))
		if err != nil {
			http.Error(w, "invalid session id", http.StatusBadRequest)
			return
		}
		lineID, err := uuid.Parse(r.PathValue("lineID"))
		if err != nil {
			http.Error(w, "invalid line id", http.StatusBadRequest)
			return
		}
		var req recordCountRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		line, err := RecordCount(r.Context(), pool, firmID, userID, sessionID, lineID, req.CountedQuantity)
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toCycleCountLineResponse(line))
	}
}

type completeCycleCountSessionRequest struct {
	Adjust bool `json:"adjust"`
}

func handleCompleteCycleCountSession(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		sessionID, err := uuid.Parse(r.PathValue("sessionID"))
		if err != nil {
			http.Error(w, "invalid session id", http.StatusBadRequest)
			return
		}
		var req completeCycleCountSessionRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		s, err := CompleteCycleCountSession(r.Context(), pool, firmID, userID, sessionID, req.Adjust)
		if err != nil {
			writeWarehouseError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toCycleCountSessionResponse(s))
	}
}
