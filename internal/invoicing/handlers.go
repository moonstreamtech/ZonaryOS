// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package invoicing

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/queryfilter"
)

const dateLayout = "2006-01-02"

// decodeJSONBody mirrors every other package's own copy.
func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires invoicing's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/invoices", auth(http.HandlerFunc(handleListInvoices(pool))))
	mux.Handle("POST /api/firms/{firmID}/invoices", auth(http.HandlerFunc(handleCreateInvoice(pool))))
	mux.Handle("GET /api/firms/{firmID}/invoices/{invoiceID}", auth(http.HandlerFunc(handleGetInvoice(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/invoices/{invoiceID}", auth(http.HandlerFunc(handleUpdateInvoiceStatus(pool))))
	// Bulk status update (Part 4 of the multi-tenant isolation hardening +
	// performance batch): see BulkUpdateInvoiceStatus's own doc comment
	// for the atomicity guarantee.
	mux.Handle("POST /api/firms/{firmID}/invoices/bulk-status", auth(http.HandlerFunc(handleBulkUpdateInvoiceStatus(pool))))

	mux.Handle("POST /api/firms/{firmID}/invoices/{invoiceID}/lines", auth(http.HandlerFunc(handleAddInvoiceLine(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/invoices/{invoiceID}/lines/{lineID}", auth(http.HandlerFunc(handleUpdateInvoiceLine(pool))))
	mux.Handle("DELETE /api/firms/{firmID}/invoices/{invoiceID}/lines/{lineID}", auth(http.HandlerFunc(handleDeleteInvoiceLine(pool))))

	mux.Handle("POST /api/firms/{firmID}/invoices/{invoiceID}/payments", auth(http.HandlerFunc(handleRecordPayment(pool))))
	mux.Handle("GET /api/firms/{firmID}/invoices/{invoiceID}/payments", auth(http.HandlerFunc(handleListPayments(pool))))

	mux.Handle("GET /api/firms/{firmID}/reports/receivables-aging", auth(http.HandlerFunc(handleReceivablesAging(pool))))
}

// writeInvoicingError maps this package's sentinel errors to the HTTP
// status that reflects why the caller isn't getting what they asked for -
// same convention as internal/logistics' writeLogisticsError.
func writeInvoicingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrInvoiceNotFound), errors.Is(err, ErrInvoiceLineNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidInvoice), errors.Is(err, ErrInvalidInvoiceLine), errors.Is(err, ErrInvalidPayment), errors.Is(err, ErrInvalidFilter):
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

func formatDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(dateLayout)
	return &s
}

type invoiceLineResponse struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Quantity    string  `json:"quantity"`
	UnitPrice   string  `json:"unitPrice"`
	TaxRate     string  `json:"taxRate"`
	TaxRateID   *string `json:"taxRateId,omitempty"`
	LineTotal   string  `json:"lineTotal"`
	ProductID   *string `json:"productId,omitempty"`
}

func toInvoiceLineResponse(l InvoiceLine) invoiceLineResponse {
	var productID *string
	if l.ProductID != nil {
		s := l.ProductID.String()
		productID = &s
	}
	var taxRateID *string
	if l.TaxRateID != nil {
		s := l.TaxRateID.String()
		taxRateID = &s
	}
	return invoiceLineResponse{
		ID: l.ID.String(), Description: l.Description, Quantity: l.Quantity, UnitPrice: l.UnitPrice,
		TaxRate: l.TaxRate, TaxRateID: taxRateID, LineTotal: l.LineTotal, ProductID: productID,
	}
}

type invoiceResponse struct {
	ID                     string                `json:"id"`
	InvoiceNumber          string                `json:"invoiceNumber"`
	CustomerID             *string               `json:"customerId,omitempty"`
	IssuedDate             string                `json:"issuedDate"`
	DueDate                *string               `json:"dueDate,omitempty"`
	Status                 string                `json:"status"`
	Subtotal               string                `json:"subtotal"`
	TaxAmount              string                `json:"taxAmount"`
	Total                  string                `json:"total"`
	Currency               string                `json:"currency"`
	Notes                  *string               `json:"notes,omitempty"`
	SourceWorkflowInstance *string               `json:"sourceWorkflowInstance,omitempty"`
	CreatedAt              string                `json:"createdAt"`
	Lines                  []invoiceLineResponse `json:"lines,omitempty"`
	// TotalPaid/Outstanding (Part 3, computed fields) are only ever
	// non-nil on handleGetInvoice's own response - see
	// Invoice.TotalPaid's own doc comment for why ListInvoices leaves
	// them unset.
	TotalPaid   *string `json:"totalPaid,omitempty"`
	Outstanding *string `json:"outstanding,omitempty"`
}

func toInvoiceResponse(inv Invoice) invoiceResponse {
	var customerID *string
	if inv.CustomerID != nil {
		s := inv.CustomerID.String()
		customerID = &s
	}
	var sourceInstance *string
	if inv.SourceWorkflowInstance != nil {
		s := inv.SourceWorkflowInstance.String()
		sourceInstance = &s
	}
	issued := inv.IssuedDate
	lines := make([]invoiceLineResponse, 0, len(inv.Lines))
	for _, l := range inv.Lines {
		lines = append(lines, toInvoiceLineResponse(l))
	}
	return invoiceResponse{
		ID: inv.ID.String(), InvoiceNumber: inv.InvoiceNumber, CustomerID: customerID,
		IssuedDate: issued.Format(dateLayout), DueDate: formatDate(inv.DueDate), Status: string(inv.Status),
		Subtotal: inv.Subtotal, TaxAmount: inv.TaxAmount, Total: inv.Total, Currency: inv.Currency,
		Notes: inv.Notes, SourceWorkflowInstance: sourceInstance, CreatedAt: inv.CreatedAt.Format(time.RFC3339), Lines: lines,
		TotalPaid: inv.TotalPaid, Outstanding: inv.Outstanding,
	}
}

func handleListInvoices(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		filters, err := queryfilter.ParseFiltersParam(r.URL.Query().Get("filters"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		opts := ListOptions{Status: InvoiceStatus(r.URL.Query().Get("status")), Filters: filters}
		invoices, err := ListInvoices(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeInvoicingError(w, err)
			return
		}

		resp := make([]invoiceResponse, 0, len(invoices))
		for _, inv := range invoices {
			resp = append(resp, toInvoiceResponse(inv))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createInvoiceLineRequest struct {
	Description string  `json:"description"`
	Quantity    string  `json:"quantity"`
	UnitPrice   string  `json:"unitPrice"`
	TaxRate     string  `json:"taxRate"`
	TaxRateID   *string `json:"taxRateId"`
	ProductID   *string `json:"productId"`
}

func toLineInput(req createInvoiceLineRequest) (InvoiceLineInput, error) {
	var productID *uuid.UUID
	if req.ProductID != nil && *req.ProductID != "" {
		id, err := uuid.Parse(*req.ProductID)
		if err != nil {
			return InvoiceLineInput{}, err
		}
		productID = &id
	}
	var taxRateID *uuid.UUID
	if req.TaxRateID != nil && *req.TaxRateID != "" {
		id, err := uuid.Parse(*req.TaxRateID)
		if err != nil {
			return InvoiceLineInput{}, err
		}
		taxRateID = &id
	}
	return InvoiceLineInput{
		Description: req.Description, Quantity: req.Quantity, UnitPrice: req.UnitPrice, TaxRate: req.TaxRate,
		TaxRateID: taxRateID, ProductID: productID,
	}, nil
}

type createInvoiceRequest struct {
	CustomerID string                     `json:"customerId"`
	IssuedDate string                     `json:"issuedDate"`
	DueDate    string                     `json:"dueDate"`
	Currency   string                     `json:"currency"`
	Notes      string                     `json:"notes"`
	Lines      []createInvoiceLineRequest `json:"lines"`
}

func handleCreateInvoice(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req createInvoiceRequest
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
		issuedDate, err := parseOptionalDate(req.IssuedDate)
		if err != nil || issuedDate == nil {
			http.Error(w, "invalid or missing issuedDate", http.StatusBadRequest)
			return
		}
		dueDate, err := parseOptionalDate(req.DueDate)
		if err != nil {
			http.Error(w, "invalid dueDate", http.StatusBadRequest)
			return
		}

		lines := make([]InvoiceLineInput, 0, len(req.Lines))
		for _, l := range req.Lines {
			li, err := toLineInput(l)
			if err != nil {
				http.Error(w, "invalid line product id", http.StatusBadRequest)
				return
			}
			lines = append(lines, li)
		}

		inv, err := CreateInvoice(r.Context(), pool, firmID, userID, customerID, *issuedDate, dueDate, req.Currency, req.Notes, lines)
		if err != nil {
			writeInvoicingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toInvoiceResponse(inv))
	}
}

func handleGetInvoice(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		invoiceID, err := uuid.Parse(r.PathValue("invoiceID"))
		if err != nil {
			http.Error(w, "invalid invoice id", http.StatusBadRequest)
			return
		}

		inv, err := GetInvoice(r.Context(), pool, firmID, userID, invoiceID)
		if err != nil {
			writeInvoicingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toInvoiceResponse(inv))
	}
}

type updateInvoiceStatusRequest struct {
	Status string `json:"status"`
}

func handleUpdateInvoiceStatus(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		invoiceID, err := uuid.Parse(r.PathValue("invoiceID"))
		if err != nil {
			http.Error(w, "invalid invoice id", http.StatusBadRequest)
			return
		}
		var req updateInvoiceStatusRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		inv, err := UpdateInvoiceStatus(r.Context(), pool, firmID, userID, invoiceID, InvoiceStatus(req.Status))
		if err != nil {
			writeInvoicingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toInvoiceResponse(inv))
	}
}

type bulkUpdateInvoiceStatusRequest struct {
	InvoiceIDs []string `json:"invoiceIds"`
	Status     string   `json:"status"`
}

// handleBulkUpdateInvoiceStatus serves POST .../invoices/bulk-status
// (Part 4) - see BulkUpdateInvoiceStatus's own doc comment.
func handleBulkUpdateInvoiceStatus(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req bulkUpdateInvoiceStatusRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		invoiceIDs := make([]uuid.UUID, 0, len(req.InvoiceIDs))
		for _, raw := range req.InvoiceIDs {
			invoiceID, err := uuid.Parse(raw)
			if err != nil {
				http.Error(w, "invalid invoice id in invoiceIds", http.StatusBadRequest)
				return
			}
			invoiceIDs = append(invoiceIDs, invoiceID)
		}

		invoices, err := BulkUpdateInvoiceStatus(r.Context(), pool, firmID, userID, invoiceIDs, InvoiceStatus(req.Status))
		if err != nil {
			writeInvoicingError(w, err)
			return
		}

		resp := make([]invoiceResponse, 0, len(invoices))
		for _, inv := range invoices {
			resp = append(resp, toInvoiceResponse(inv))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleAddInvoiceLine(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		invoiceID, err := uuid.Parse(r.PathValue("invoiceID"))
		if err != nil {
			http.Error(w, "invalid invoice id", http.StatusBadRequest)
			return
		}
		var req createInvoiceLineRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		li, err := toLineInput(req)
		if err != nil {
			http.Error(w, "invalid line product id", http.StatusBadRequest)
			return
		}

		inv, err := AddInvoiceLine(r.Context(), pool, firmID, userID, invoiceID, li)
		if err != nil {
			writeInvoicingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toInvoiceResponse(inv))
	}
}

func handleUpdateInvoiceLine(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		invoiceID, err := uuid.Parse(r.PathValue("invoiceID"))
		if err != nil {
			http.Error(w, "invalid invoice id", http.StatusBadRequest)
			return
		}
		lineID, err := uuid.Parse(r.PathValue("lineID"))
		if err != nil {
			http.Error(w, "invalid line id", http.StatusBadRequest)
			return
		}
		var req createInvoiceLineRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		li, err := toLineInput(req)
		if err != nil {
			http.Error(w, "invalid line product id", http.StatusBadRequest)
			return
		}

		inv, err := UpdateInvoiceLine(r.Context(), pool, firmID, userID, invoiceID, lineID, li)
		if err != nil {
			writeInvoicingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toInvoiceResponse(inv))
	}
}

func handleDeleteInvoiceLine(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		invoiceID, err := uuid.Parse(r.PathValue("invoiceID"))
		if err != nil {
			http.Error(w, "invalid invoice id", http.StatusBadRequest)
			return
		}
		lineID, err := uuid.Parse(r.PathValue("lineID"))
		if err != nil {
			http.Error(w, "invalid line id", http.StatusBadRequest)
			return
		}

		inv, err := DeleteInvoiceLine(r.Context(), pool, firmID, userID, invoiceID, lineID)
		if err != nil {
			writeInvoicingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toInvoiceResponse(inv))
	}
}

type paymentResponse struct {
	ID        string  `json:"id"`
	InvoiceID *string `json:"invoiceId,omitempty"`
	Amount    string  `json:"amount"`
	Currency  string  `json:"currency"`
	PaidAt    string  `json:"paidAt"`
	Method    *string `json:"method,omitempty"`
	Reference *string `json:"reference,omitempty"`
	CreatedAt string  `json:"createdAt"`
}

func toPaymentResponse(p Payment) paymentResponse {
	var invoiceID *string
	if p.InvoiceID != nil {
		s := p.InvoiceID.String()
		invoiceID = &s
	}
	return paymentResponse{
		ID: p.ID.String(), InvoiceID: invoiceID, Amount: p.Amount, Currency: p.Currency,
		PaidAt: p.PaidAt.Format(time.RFC3339), Method: p.Method, Reference: p.Reference, CreatedAt: p.CreatedAt.Format(time.RFC3339),
	}
}

type recordPaymentRequest struct {
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	PaidAt    string `json:"paidAt"`
	Method    string `json:"method"`
	Reference string `json:"reference"`
}

type recordPaymentResponse struct {
	Payment paymentResponse `json:"payment"`
	Invoice invoiceResponse `json:"invoice"`
}

func handleRecordPayment(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		invoiceID, err := uuid.Parse(r.PathValue("invoiceID"))
		if err != nil {
			http.Error(w, "invalid invoice id", http.StatusBadRequest)
			return
		}
		var req recordPaymentRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		paidAt, err := time.Parse(time.RFC3339, req.PaidAt)
		if err != nil {
			http.Error(w, "invalid paidAt (must be RFC3339)", http.StatusBadRequest)
			return
		}

		payment, inv, err := RecordPayment(r.Context(), pool, firmID, userID, invoiceID, RecordPaymentInput{
			Amount: req.Amount, Currency: req.Currency, PaidAt: paidAt, Method: req.Method, Reference: req.Reference,
		})
		if err != nil {
			writeInvoicingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(recordPaymentResponse{Payment: toPaymentResponse(payment), Invoice: toInvoiceResponse(inv)})
	}
}

func handleListPayments(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		invoiceID, err := uuid.Parse(r.PathValue("invoiceID"))
		if err != nil {
			http.Error(w, "invalid invoice id", http.StatusBadRequest)
			return
		}

		payments, err := ListPayments(r.Context(), pool, firmID, userID, invoiceID)
		if err != nil {
			writeInvoicingError(w, err)
			return
		}

		resp := make([]paymentResponse, 0, len(payments))
		for _, p := range payments {
			resp = append(resp, toPaymentResponse(p))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type agingBucketResponse struct {
	Label       string `json:"label"`
	Count       int    `json:"count"`
	Outstanding string `json:"outstanding"`
}

func handleReceivablesAging(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		buckets, err := ReceivablesAging(r.Context(), pool, firmID, userID)
		if err != nil {
			writeInvoicingError(w, err)
			return
		}

		resp := make([]agingBucketResponse, 0, len(buckets))
		for _, b := range buckets {
			resp = append(resp, agingBucketResponse{Label: b.Label, Count: b.Count, Outstanding: b.Outstanding})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
