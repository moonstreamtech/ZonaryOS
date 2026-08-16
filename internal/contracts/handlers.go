// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package contracts

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
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

// RegisterRoutes wires internal/contracts' HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/contracts", auth(http.HandlerFunc(handleListContracts(pool))))
	mux.Handle("POST /api/firms/{firmID}/contracts", auth(http.HandlerFunc(handleCreateContract(pool))))
	// A literal "expiring" path segment collides with {contractID} unless
	// registered first - Go's net/http.ServeMux (1.22+ pattern matching)
	// prefers the more specific literal match over the wildcard
	// regardless of registration order, so this ordering is for
	// readability, not correctness (same non-issue documented elsewhere
	// in this codebase, e.g. internal/warehouse/handlers.go's own
	// RegisterRoutes comment about a similar sibling-path shape).
	mux.Handle("GET /api/firms/{firmID}/contracts/expiring", auth(http.HandlerFunc(handleListExpiringContracts(pool))))
	mux.Handle("GET /api/firms/{firmID}/contracts/{contractID}", auth(http.HandlerFunc(handleGetContract(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/contracts/{contractID}", auth(http.HandlerFunc(handleUpdateContract(pool))))

	mux.Handle("GET /api/firms/{firmID}/contracts/{contractID}/documents", auth(http.HandlerFunc(handleListDocuments(pool))))
	mux.Handle("POST /api/firms/{firmID}/contracts/{contractID}/documents", auth(http.HandlerFunc(handleCreateDocument(pool))))
}

// writeContractsError maps this package's sentinel errors to the HTTP
// status that reflects why the caller isn't getting what they asked for -
// same convention as internal/asset's writeAssetError.
func writeContractsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrContractNotFound), errors.Is(err, ErrDocumentNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidContract), errors.Is(err, ErrDuplicateReferenceNumber), errors.Is(err, ErrInvalidDocument):
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

func formatDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format("2006-01-02")
	return &s
}

func uuidString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

// ---------------------------------------------------------------------
// Contracts
// ---------------------------------------------------------------------

type contractResponse struct {
	ID                string         `json:"id"`
	ReferenceNumber   string         `json:"referenceNumber"`
	Title             string         `json:"title"`
	Type              string         `json:"type"`
	CounterpartyName  string         `json:"counterpartyName"`
	CounterpartyID    *string        `json:"counterpartyId,omitempty"`
	CounterpartyType  *string        `json:"counterpartyType,omitempty"`
	Status            string         `json:"status"`
	Value             *string        `json:"value,omitempty"`
	Currency          string         `json:"currency"`
	StartDate         *string        `json:"startDate,omitempty"`
	EndDate           *string        `json:"endDate,omitempty"`
	AutoRenewal       bool           `json:"autoRenewal"`
	RenewalNoticeDays *int           `json:"renewalNoticeDays,omitempty"`
	SignedAt          *string        `json:"signedAt,omitempty"`
	SignedBy          *string        `json:"signedBy,omitempty"`
	Notes             *string        `json:"notes,omitempty"`
	CustomFields      map[string]any `json:"customFields"`
	CreatedAt         string         `json:"createdAt"`
}

func toContractResponse(c Contract) contractResponse {
	var signedAt *string
	if c.SignedAt != nil {
		s := c.SignedAt.Format(time.RFC3339)
		signedAt = &s
	}
	return contractResponse{
		ID: c.ID.String(), ReferenceNumber: c.ReferenceNumber, Title: c.Title, Type: c.Type,
		CounterpartyName: c.CounterpartyName, CounterpartyID: uuidString(c.CounterpartyID), CounterpartyType: c.CounterpartyType,
		Status: c.Status, Value: c.Value, Currency: c.Currency, StartDate: formatDate(c.StartDate), EndDate: formatDate(c.EndDate),
		AutoRenewal: c.AutoRenewal, RenewalNoticeDays: c.RenewalNoticeDays, SignedAt: signedAt, SignedBy: uuidString(c.SignedBy),
		Notes: c.Notes, CustomFields: c.CustomFields, CreatedAt: c.CreatedAt.Format(time.RFC3339),
	}
}

func handleListContracts(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		list, err := ListContracts(r.Context(), pool, firmID, userID)
		if err != nil {
			writeContractsError(w, err)
			return
		}
		resp := make([]contractResponse, 0, len(list))
		for _, c := range list {
			resp = append(resp, toContractResponse(c))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleListExpiringContracts(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		days := 30
		if raw := r.URL.Query().Get("days"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 {
				http.Error(w, "invalid days", http.StatusBadRequest)
				return
			}
			days = parsed
		}
		list, err := ListExpiringContracts(r.Context(), pool, firmID, userID, days)
		if err != nil {
			writeContractsError(w, err)
			return
		}
		resp := make([]contractResponse, 0, len(list))
		for _, c := range list {
			resp = append(resp, toContractResponse(c))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type contractRequest struct {
	ReferenceNumber   string         `json:"referenceNumber"`
	Title             string         `json:"title"`
	Type              string         `json:"type"`
	CounterpartyName  string         `json:"counterpartyName"`
	CounterpartyID    *uuid.UUID     `json:"counterpartyId"`
	CounterpartyType  string         `json:"counterpartyType"`
	Status            string         `json:"status"`
	Value             *string        `json:"value"`
	Currency          string         `json:"currency"`
	StartDate         string         `json:"startDate"`
	EndDate           string         `json:"endDate"`
	AutoRenewal       bool           `json:"autoRenewal"`
	RenewalNoticeDays *int           `json:"renewalNoticeDays"`
	Notes             string         `json:"notes"`
	CustomFields      map[string]any `json:"customFields"`
}

func handleCreateContract(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req contractRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		c, err := CreateContract(r.Context(), pool, firmID, userID, CreateContractInput{
			ReferenceNumber: req.ReferenceNumber, Title: req.Title, Type: req.Type, CounterpartyName: req.CounterpartyName,
			CounterpartyID: req.CounterpartyID, CounterpartyType: req.CounterpartyType, Status: req.Status, Value: req.Value,
			Currency: req.Currency, StartDate: req.StartDate, EndDate: req.EndDate, AutoRenewal: req.AutoRenewal,
			RenewalNoticeDays: req.RenewalNoticeDays, Notes: req.Notes, CustomFields: req.CustomFields,
		})
		if err != nil {
			writeContractsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toContractResponse(c))
	}
}

func handleGetContract(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		contractID, err := uuid.Parse(r.PathValue("contractID"))
		if err != nil {
			http.Error(w, "invalid contract id", http.StatusBadRequest)
			return
		}
		c, err := GetContract(r.Context(), pool, firmID, userID, contractID)
		if err != nil {
			writeContractsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toContractResponse(c))
	}
}

type updateContractRequest struct {
	Title             *string        `json:"title"`
	Status            *string        `json:"status"`
	CounterpartyID    *uuid.UUID     `json:"counterpartyId"`
	HasCounterpartyID bool           `json:"hasCounterpartyId"`
	Value             *string        `json:"value"`
	StartDate         *string        `json:"startDate"`
	EndDate           *string        `json:"endDate"`
	AutoRenewal       *bool          `json:"autoRenewal"`
	RenewalNoticeDays *int           `json:"renewalNoticeDays"`
	SignedAt          *string        `json:"signedAt"`
	SignedBy          *uuid.UUID     `json:"signedBy"`
	Notes             *string        `json:"notes"`
	CustomFields      map[string]any `json:"customFields"`
}

func handleUpdateContract(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		contractID, err := uuid.Parse(r.PathValue("contractID"))
		if err != nil {
			http.Error(w, "invalid contract id", http.StatusBadRequest)
			return
		}
		var req updateContractRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		var signedAt *time.Time
		if req.SignedAt != nil && *req.SignedAt != "" {
			t, err := time.Parse(time.RFC3339, *req.SignedAt)
			if err != nil {
				http.Error(w, "invalid signedAt (expected RFC3339)", http.StatusBadRequest)
				return
			}
			signedAt = &t
		}
		c, err := UpdateContract(r.Context(), pool, firmID, userID, contractID, UpdateContractInput{
			Title: req.Title, Status: req.Status, CounterpartyID: req.CounterpartyID, HasCounterpartyID: req.HasCounterpartyID,
			Value: req.Value, StartDate: req.StartDate, EndDate: req.EndDate, AutoRenewal: req.AutoRenewal,
			RenewalNoticeDays: req.RenewalNoticeDays, SignedAt: signedAt, SignedBy: req.SignedBy, Notes: req.Notes,
			CustomFields: req.CustomFields,
		})
		if err != nil {
			writeContractsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toContractResponse(c))
	}
}

// ---------------------------------------------------------------------
// Documents
// ---------------------------------------------------------------------

type documentResponse struct {
	ID          string  `json:"id"`
	ContractID  string  `json:"contractId"`
	Name        string  `json:"name"`
	DocumentURL *string `json:"documentUrl,omitempty"`
	Type        string  `json:"type"`
	UploadedAt  string  `json:"uploadedAt"`
	UploadedBy  string  `json:"uploadedBy"`
}

func toDocumentResponse(d Document) documentResponse {
	return documentResponse{
		ID: d.ID.String(), ContractID: d.ContractID.String(), Name: d.Name, DocumentURL: d.DocumentURL,
		Type: d.Type, UploadedAt: d.UploadedAt.Format(time.RFC3339), UploadedBy: d.UploadedBy.String(),
	}
}

func handleListDocuments(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		contractID, err := uuid.Parse(r.PathValue("contractID"))
		if err != nil {
			http.Error(w, "invalid contract id", http.StatusBadRequest)
			return
		}
		list, err := ListDocuments(r.Context(), pool, firmID, userID, contractID)
		if err != nil {
			writeContractsError(w, err)
			return
		}
		resp := make([]documentResponse, 0, len(list))
		for _, d := range list {
			resp = append(resp, toDocumentResponse(d))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type documentRequest struct {
	Name        string `json:"name"`
	DocumentURL string `json:"documentUrl"`
	Type        string `json:"type"`
}

func handleCreateDocument(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		contractID, err := uuid.Parse(r.PathValue("contractID"))
		if err != nil {
			http.Error(w, "invalid contract id", http.StatusBadRequest)
			return
		}
		var req documentRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		d, err := CreateDocument(r.Context(), pool, firmID, userID, contractID, CreateDocumentInput{
			Name: req.Name, DocumentURL: req.DocumentURL, Type: req.Type,
		})
		if err != nil {
			writeContractsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toDocumentResponse(d))
	}
}
