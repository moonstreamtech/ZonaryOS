// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package inventory

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

// RegisterRoutes wires inventory's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/products", auth(http.HandlerFunc(handleListProducts(pool))))
	mux.Handle("POST /api/firms/{firmID}/products", auth(http.HandlerFunc(handleCreateProduct(pool))))
	mux.Handle("GET /api/firms/{firmID}/products/{productID}", auth(http.HandlerFunc(handleGetProduct(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/products/{productID}", auth(http.HandlerFunc(handleUpdateProduct(pool))))
	mux.Handle("GET /api/firms/{firmID}/products/{productID}/stock", auth(http.HandlerFunc(handleGetStock(pool))))
	mux.Handle("GET /api/firms/{firmID}/stock-movements", auth(http.HandlerFunc(handleListStockMovements(pool))))

	mux.Handle("GET /api/firms/{firmID}/suppliers", auth(http.HandlerFunc(handleListSuppliers(pool))))
	mux.Handle("POST /api/firms/{firmID}/suppliers", auth(http.HandlerFunc(handleCreateSupplier(pool))))
	mux.Handle("GET /api/firms/{firmID}/suppliers/{supplierID}", auth(http.HandlerFunc(handleGetSupplier(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/suppliers/{supplierID}", auth(http.HandlerFunc(handleUpdateSupplier(pool))))
}

// writeInventoryError maps this package's sentinel errors to the HTTP
// status that reflects why the caller isn't getting what they asked for -
// same convention as internal/accounting's writeAccountingError /
// internal/hr's writeHRError.
func writeInventoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrProductNotFound), errors.Is(err, ErrSupplierNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidProduct), errors.Is(err, ErrInvalidSupplier), errors.Is(err, ErrProductSKUExists):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrNoDefaultLocation):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type productResponse struct {
	ID           string         `json:"id"`
	SKU          string         `json:"sku"`
	Name         string         `json:"name"`
	Description  *string        `json:"description,omitempty"`
	Unit         string         `json:"unit"`
	UnitPrice    *string        `json:"unitPrice,omitempty"`
	CostPrice    *string        `json:"costPrice,omitempty"`
	TaxRate      *string        `json:"taxRate,omitempty"`
	Category     *string        `json:"category,omitempty"`
	MinQuantity  string         `json:"minQuantity"`
	CustomFields map[string]any `json:"customFields"`
	IsActive     bool           `json:"isActive"`
	CreatedAt    string         `json:"createdAt"`
	UpdatedAt    string         `json:"updatedAt"`
	// StockQuantity (Part 3, computed field) is only ever non-nil on
	// handleGetProduct's own response - see Product.StockQuantity's own
	// doc comment for why ListProducts leaves it unset.
	StockQuantity *string `json:"stockQuantity,omitempty"`
}

// productResponseFields is handleListProducts' own ?fields= allowlist
// (mobile API optimization batch, Part 1) - every productResponse json
// tag except stockQuantity, which ListProducts never populates anyway
// (see that field's own doc comment).
var productResponseFields = map[string]bool{
	"id": true, "sku": true, "name": true, "description": true, "unit": true, "unitPrice": true,
	"costPrice": true, "taxRate": true, "category": true, "minQuantity": true, "customFields": true,
	"isActive": true, "createdAt": true, "updatedAt": true, "stockQuantity": true,
}

func toProductResponse(p Product) productResponse {
	return productResponse{
		ID: p.ID.String(), SKU: p.SKU, Name: p.Name, Description: p.Description, Unit: p.Unit,
		UnitPrice: p.UnitPrice, CostPrice: p.CostPrice, TaxRate: p.TaxRate, Category: p.Category,
		MinQuantity: p.MinQuantity, CustomFields: p.CustomFields, IsActive: p.IsActive,
		CreatedAt: p.CreatedAt.Format(time.RFC3339), UpdatedAt: p.UpdatedAt.Format(time.RFC3339), StockQuantity: p.StockQuantity,
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

func handleListProducts(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		updatedSince, err := apifields.ParseUpdatedSince(r.URL.Query().Get("updated_since"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		opts := ListProductsOptions{ActiveOnly: r.URL.Query().Get("activeOnly") == "true", Search: r.URL.Query().Get("q"), UpdatedSince: updatedSince}
		products, err := ListProducts(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeInventoryError(w, err)
			return
		}

		resp := make([]productResponse, 0, len(products))
		for _, p := range products {
			resp = append(resp, toProductResponse(p))
		}

		projected, err := apifields.Project(resp, apifields.ParseFields(r.URL.Query().Get("fields")), productResponseFields)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// #nosec G705 -- projected is JSON we produced ourselves
		// (apifields.Project marshals only this handler's own
		// productResponse values, filtered to productResponseFields' own
		// closed allowlist) - never raw request input echoed back.
		_, _ = w.Write(projected)
	}
}

type createProductRequest struct {
	SKU          string         `json:"sku"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Unit         string         `json:"unit"`
	UnitPrice    string         `json:"unitPrice"`
	CostPrice    string         `json:"costPrice"`
	TaxRate      string         `json:"taxRate"`
	Category     string         `json:"category"`
	MinQuantity  string         `json:"minQuantity"`
	CustomFields map[string]any `json:"customFields"`
}

func handleCreateProduct(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createProductRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		product, err := CreateProduct(r.Context(), pool, firmID, userID, CreateProductInput{
			SKU: req.SKU, Name: req.Name, Description: req.Description, Unit: req.Unit,
			UnitPrice: req.UnitPrice, CostPrice: req.CostPrice, TaxRate: req.TaxRate,
			Category: req.Category, MinQuantity: req.MinQuantity, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeInventoryError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toProductResponse(product))
	}
}

func handleGetProduct(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		productID, err := uuid.Parse(r.PathValue("productID"))
		if err != nil {
			http.Error(w, "invalid product id", http.StatusBadRequest)
			return
		}

		product, err := GetProduct(r.Context(), pool, firmID, userID, productID)
		if err != nil {
			writeInventoryError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toProductResponse(product))
	}
}

type updateProductRequest struct {
	Name         *string        `json:"name"`
	Description  *string        `json:"description"`
	Unit         *string        `json:"unit"`
	UnitPrice    *string        `json:"unitPrice"`
	CostPrice    *string        `json:"costPrice"`
	TaxRate      *string        `json:"taxRate"`
	Category     *string        `json:"category"`
	MinQuantity  *string        `json:"minQuantity"`
	IsActive     *bool          `json:"isActive"`
	CustomFields map[string]any `json:"customFields"`
}

func handleUpdateProduct(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		productID, err := uuid.Parse(r.PathValue("productID"))
		if err != nil {
			http.Error(w, "invalid product id", http.StatusBadRequest)
			return
		}
		var req updateProductRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		product, err := UpdateProduct(r.Context(), pool, firmID, userID, productID, ProductUpdate{
			Name: req.Name, Description: req.Description, Unit: req.Unit, UnitPrice: req.UnitPrice,
			CostPrice: req.CostPrice, TaxRate: req.TaxRate, Category: req.Category,
			MinQuantity: req.MinQuantity, IsActive: req.IsActive, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeInventoryError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toProductResponse(product))
	}
}

type stockLevelResponse struct {
	ProductID        string `json:"productId"`
	LocationID       string `json:"locationId"`
	LocationCode     string `json:"locationCode"`
	Quantity         string `json:"quantity"`
	ReservedQuantity string `json:"reservedQuantity"`
	UpdatedAt        string `json:"updatedAt"`
}

func handleGetStock(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		productID, err := uuid.Parse(r.PathValue("productID"))
		if err != nil {
			http.Error(w, "invalid product id", http.StatusBadRequest)
			return
		}

		levels, err := GetStock(r.Context(), pool, firmID, userID, productID)
		if err != nil {
			writeInventoryError(w, err)
			return
		}

		resp := make([]stockLevelResponse, 0, len(levels))
		for _, l := range levels {
			resp = append(resp, stockLevelResponse{
				ProductID: l.ProductID.String(), LocationID: l.LocationID.String(), LocationCode: l.LocationCode,
				Quantity: l.Quantity, ReservedQuantity: l.ReservedQuantity, UpdatedAt: l.UpdatedAt.Format(time.RFC3339),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type stockMovementResponse struct {
	ID             string  `json:"id"`
	ProductID      string  `json:"productId"`
	LocationID     string  `json:"locationId"`
	LocationCode   string  `json:"locationCode"`
	QuantityChange string  `json:"quantityChange"`
	Reason         string  `json:"reason"`
	SourceType     string  `json:"sourceType,omitempty"`
	SourceID       *string `json:"sourceId,omitempty"`
	CreatedAt      string  `json:"createdAt"`
}

type listStockMovementsResponse struct {
	Movements []stockMovementResponse `json:"movements"`
	Total     int                     `json:"total"`
}

func handleListStockMovements(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		opts := ListMovementsOptions{}
		if limit, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
			opts.Limit = limit
		}
		if offset, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil {
			opts.Offset = offset
		}
		if productIDParam := r.URL.Query().Get("productId"); productIDParam != "" {
			productID, err := uuid.Parse(productIDParam)
			if err != nil {
				http.Error(w, "invalid productId", http.StatusBadRequest)
				return
			}
			opts.ProductID = &productID
		}

		result, err := ListStockMovements(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeInventoryError(w, err)
			return
		}

		resp := listStockMovementsResponse{Movements: make([]stockMovementResponse, 0, len(result.Movements)), Total: result.Total}
		for _, m := range result.Movements {
			var sourceID *string
			if m.SourceID != nil {
				s := m.SourceID.String()
				sourceID = &s
			}
			resp.Movements = append(resp.Movements, stockMovementResponse{
				ID: m.ID.String(), ProductID: m.ProductID.String(), LocationID: m.LocationID.String(), LocationCode: m.LocationCode,
				QuantityChange: m.QuantityChange, Reason: m.Reason, SourceType: m.SourceType,
				SourceID: sourceID, CreatedAt: m.CreatedAt.Format(time.RFC3339),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type supplierResponse struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	ContactName  *string        `json:"contactName,omitempty"`
	Email        *string        `json:"email,omitempty"`
	Phone        *string        `json:"phone,omitempty"`
	Address      *string        `json:"address,omitempty"`
	Currency     string         `json:"currency"`
	PaymentTerms *string        `json:"paymentTerms,omitempty"`
	CustomFields map[string]any `json:"customFields"`
	IsActive     bool           `json:"isActive"`
	CreatedAt    string         `json:"createdAt"`
}

func toSupplierResponse(s Supplier) supplierResponse {
	return supplierResponse{
		ID: s.ID.String(), Name: s.Name, ContactName: s.ContactName, Email: s.Email, Phone: s.Phone,
		Address: s.Address, Currency: s.Currency, PaymentTerms: s.PaymentTerms,
		CustomFields: s.CustomFields, IsActive: s.IsActive, CreatedAt: s.CreatedAt.Format(time.RFC3339),
	}
}

func handleListSuppliers(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		opts := ListSuppliersOptions{ActiveOnly: r.URL.Query().Get("activeOnly") == "true", Search: r.URL.Query().Get("q")}
		suppliers, err := ListSuppliers(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeInventoryError(w, err)
			return
		}

		resp := make([]supplierResponse, 0, len(suppliers))
		for _, s := range suppliers {
			resp = append(resp, toSupplierResponse(s))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createSupplierRequest struct {
	Name         string         `json:"name"`
	ContactName  string         `json:"contactName"`
	Email        string         `json:"email"`
	Phone        string         `json:"phone"`
	Address      string         `json:"address"`
	Currency     string         `json:"currency"`
	PaymentTerms string         `json:"paymentTerms"`
	CustomFields map[string]any `json:"customFields"`
}

func handleCreateSupplier(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createSupplierRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		supplier, err := CreateSupplier(r.Context(), pool, firmID, userID, CreateSupplierInput{
			Name: req.Name, ContactName: req.ContactName, Email: req.Email, Phone: req.Phone,
			Address: req.Address, Currency: req.Currency, PaymentTerms: req.PaymentTerms, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeInventoryError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toSupplierResponse(supplier))
	}
}

func handleGetSupplier(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		supplierID, err := uuid.Parse(r.PathValue("supplierID"))
		if err != nil {
			http.Error(w, "invalid supplier id", http.StatusBadRequest)
			return
		}

		supplier, err := GetSupplier(r.Context(), pool, firmID, userID, supplierID)
		if err != nil {
			writeInventoryError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toSupplierResponse(supplier))
	}
}

type updateSupplierRequest struct {
	Name         *string        `json:"name"`
	ContactName  *string        `json:"contactName"`
	Email        *string        `json:"email"`
	Phone        *string        `json:"phone"`
	Address      *string        `json:"address"`
	Currency     *string        `json:"currency"`
	PaymentTerms *string        `json:"paymentTerms"`
	IsActive     *bool          `json:"isActive"`
	CustomFields map[string]any `json:"customFields"`
}

func handleUpdateSupplier(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		supplierID, err := uuid.Parse(r.PathValue("supplierID"))
		if err != nil {
			http.Error(w, "invalid supplier id", http.StatusBadRequest)
			return
		}
		var req updateSupplierRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		supplier, err := UpdateSupplier(r.Context(), pool, firmID, userID, supplierID, SupplierUpdate{
			Name: req.Name, ContactName: req.ContactName, Email: req.Email, Phone: req.Phone,
			Address: req.Address, Currency: req.Currency, PaymentTerms: req.PaymentTerms,
			IsActive: req.IsActive, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeInventoryError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toSupplierResponse(supplier))
	}
}
