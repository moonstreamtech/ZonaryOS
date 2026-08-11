// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package accounting

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
	"github.com/moonstreamtech/ZonaryOS/internal/queryfilter"
)

// maxListLimit caps the ?limit= query param on GET .../journal-entries -
// same numeric cap and reasoning as internal/auditlog's maxListLimit/
// internal/workflow's maxListInstancesLimit.
const maxListLimit = 200

// decodeJSONBody decodes r's body into v - identical contract to
// internal/workflow's own decodeJSONBody (a missing/empty body is a no-op,
// not an error), redeclared here rather than imported since it's an
// unexported helper in that package.
func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// RegisterRoutes wires the financial management core's HTTP endpoints
// into mux, same bearer-token auth middleware and /api/firms/{firmID}/...
// convention as every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/accounts", auth(http.HandlerFunc(handleListAccounts(pool))))
	mux.Handle("POST /api/firms/{firmID}/accounts", auth(http.HandlerFunc(handleCreateAccount(pool))))
	mux.Handle("PATCH /api/firms/{firmID}/accounts/{accountID}", auth(http.HandlerFunc(handleUpdateAccount(pool))))
	mux.Handle("GET /api/firms/{firmID}/accounts/{accountID}/balance", auth(http.HandlerFunc(handleAccountBalance(pool))))
	mux.Handle("GET /api/firms/{firmID}/journal-entries", auth(http.HandlerFunc(handleListJournalEntries(pool))))
	mux.Handle("POST /api/firms/{firmID}/journal-entries", auth(http.HandlerFunc(handlePostJournalEntry(pool))))
	mux.Handle("GET /api/firms/{firmID}/reports/pnl", auth(http.HandlerFunc(handlePnLReport(pool))))
	mux.Handle("GET /api/firms/{firmID}/reports/balance-sheet", auth(http.HandlerFunc(handleBalanceSheetReport(pool))))
}

// writeAccountingError maps this package's sentinel errors to the HTTP
// status that reflects why the caller isn't getting what they asked for -
// same convention as internal/workflow's writeEngineError.
func writeAccountingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrAccountNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidAccount), errors.Is(err, ErrInvalidJournalEntry), errors.Is(err, ErrAccountInactive), errors.Is(err, ErrUnbalancedEntry), errors.Is(err, ErrInvalidFilter):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrAccountCodeExists):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type accountResponse struct {
	ID        string  `json:"id"`
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	ParentID  *string `json:"parentId,omitempty"`
	Currency  string  `json:"currency"`
	IsActive  bool    `json:"isActive"`
	CreatedAt string  `json:"createdAt"`
}

func toAccountResponse(a Account) accountResponse {
	resp := accountResponse{
		ID:        a.ID.String(),
		Code:      a.Code,
		Name:      a.Name,
		Type:      string(a.Type),
		Currency:  a.Currency,
		IsActive:  a.IsActive,
		CreatedAt: a.CreatedAt.Format(time.RFC3339),
	}
	if a.ParentID != nil {
		id := a.ParentID.String()
		resp.ParentID = &id
	}
	return resp
}

func handleListAccounts(pool *pgxpool.Pool) http.HandlerFunc {
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

		accounts, err := ListAccounts(r.Context(), pool, firmID, userID)
		if err != nil {
			writeAccountingError(w, err)
			return
		}

		resp := make([]accountResponse, 0, len(accounts))
		for _, a := range accounts {
			resp = append(resp, toAccountResponse(a))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createAccountRequest struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	ParentID *string `json:"parentId"`
	Currency string  `json:"currency"`
}

func handleCreateAccount(pool *pgxpool.Pool) http.HandlerFunc {
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
		var req createAccountRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		var parentID *uuid.UUID
		if req.ParentID != nil && *req.ParentID != "" {
			parsed, err := uuid.Parse(*req.ParentID)
			if err != nil {
				http.Error(w, "invalid parent id", http.StatusBadRequest)
				return
			}
			parentID = &parsed
		}

		account, err := CreateAccount(r.Context(), pool, firmID, userID, req.Code, req.Name, AccountType(req.Type), parentID, req.Currency)
		if err != nil {
			writeAccountingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toAccountResponse(account))
	}
}

type updateAccountRequest struct {
	Name     *string `json:"name"`
	IsActive *bool   `json:"isActive"`
}

func handleUpdateAccount(pool *pgxpool.Pool) http.HandlerFunc {
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
		accountID, err := uuid.Parse(r.PathValue("accountID"))
		if err != nil {
			http.Error(w, "invalid account id", http.StatusBadRequest)
			return
		}
		var req updateAccountRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		account, err := UpdateAccount(r.Context(), pool, firmID, userID, accountID, AccountUpdate{Name: req.Name, IsActive: req.IsActive})
		if err != nil {
			writeAccountingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toAccountResponse(account))
	}
}

func handleAccountBalance(pool *pgxpool.Pool) http.HandlerFunc {
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
		accountID, err := uuid.Parse(r.PathValue("accountID"))
		if err != nil {
			http.Error(w, "invalid account id", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		balance, err := GetAccountBalance(r.Context(), pool, firmID, userID, accountID)
		if err != nil {
			writeAccountingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"balance": balance})
	}
}

type journalLineResponse struct {
	AccountID   string `json:"accountId"`
	AccountCode string `json:"accountCode"`
	AccountName string `json:"accountName"`
	Side        string `json:"side"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
}

type journalEntryResponse struct {
	ID          string                `json:"id"`
	PostedAt    string                `json:"postedAt"`
	Description string                `json:"description"`
	SourceType  string                `json:"sourceType,omitempty"`
	SourceID    *string               `json:"sourceId,omitempty"`
	CreatedBy   string                `json:"createdBy"`
	Lines       []journalLineResponse `json:"lines"`
}

func toJournalEntryResponse(e JournalEntry) journalEntryResponse {
	resp := journalEntryResponse{
		ID:          e.ID.String(),
		PostedAt:    e.PostedAt.Format(time.RFC3339),
		Description: e.Description,
		SourceType:  e.SourceType,
		CreatedBy:   e.CreatedBy.String(),
		Lines:       make([]journalLineResponse, 0, len(e.Lines)),
	}
	if e.SourceID != nil {
		id := e.SourceID.String()
		resp.SourceID = &id
	}
	for _, l := range e.Lines {
		resp.Lines = append(resp.Lines, journalLineResponse{
			AccountID:   l.AccountID.String(),
			AccountCode: l.AccountCode,
			AccountName: l.AccountName,
			Side:        string(l.Side),
			Amount:      l.Amount,
			Currency:    l.Currency,
		})
	}
	return resp
}

func handleListJournalEntries(pool *pgxpool.Pool) http.HandlerFunc {
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
		opts, err := parseListOptions(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		result, err := ListJournalEntries(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeAccountingError(w, err)
			return
		}

		resp := make([]journalEntryResponse, 0, len(result.Entries))
		for _, e := range result.Entries {
			resp = append(resp, toJournalEntryResponse(e))
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(result.Total))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// parseListOptions reads ?limit=/?offset=/?filters= off r into
// ListOptions - same convention as internal/auditlog's parseListOptions;
// ?filters= is a JSON-encoded `[{field, op, value}, ...]` array (Part 2
// of the search/filtering/enrichment batch).
func parseListOptions(r *http.Request) (ListOptions, error) {
	q := r.URL.Query()
	var opts ListOptions
	if raw := q.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 || limit > maxListLimit {
			return ListOptions{}, errors.New("invalid limit")
		}
		opts.Limit = limit
	}
	if raw := q.Get("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return ListOptions{}, errors.New("invalid offset")
		}
		opts.Offset = offset
	}
	filters, err := queryfilter.ParseFiltersParam(q.Get("filters"))
	if err != nil {
		return ListOptions{}, err
	}
	opts.Filters = filters
	return opts, nil
}

type postJournalLineRequest struct {
	AccountCode string `json:"accountCode"`
	Side        string `json:"side"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
}

type postJournalEntryRequest struct {
	Description string                   `json:"description"`
	Lines       []postJournalLineRequest `json:"lines"`
}

func handlePostJournalEntry(pool *pgxpool.Pool) http.HandlerFunc {
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
		var req postJournalEntryRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		lines := make([]LineInput, 0, len(req.Lines))
		for _, l := range req.Lines {
			lines = append(lines, LineInput{AccountCode: l.AccountCode, Side: Side(l.Side), Amount: l.Amount, Currency: l.Currency})
		}

		entryID, err := PostJournalEntry(r.Context(), pool, firmID, userID, req.Description, lines)
		if err != nil {
			writeAccountingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": entryID.String()})
	}
}

// reportLineResponse is ReportLine's JSON wire shape.
type reportLineResponse struct {
	AccountID string `json:"accountId"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Amount    string `json:"amount"`
}

func toReportLineResponses(lines []ReportLine) []reportLineResponse {
	resp := make([]reportLineResponse, 0, len(lines))
	for _, l := range lines {
		resp = append(resp, reportLineResponse{AccountID: l.AccountID.String(), Code: l.Code, Name: l.Name, Amount: l.Amount})
	}
	return resp
}

type pnlReportResponse struct {
	From          *string              `json:"from,omitempty"`
	To            *string              `json:"to,omitempty"`
	Revenue       []reportLineResponse `json:"revenue"`
	Expenses      []reportLineResponse `json:"expenses"`
	TotalRevenue  string               `json:"totalRevenue"`
	TotalExpenses string               `json:"totalExpenses"`
	NetIncome     string               `json:"netIncome"`
}

func toPnLReportResponse(r PnLReport) pnlReportResponse {
	resp := pnlReportResponse{
		Revenue:       toReportLineResponses(r.Revenue),
		Expenses:      toReportLineResponses(r.Expenses),
		TotalRevenue:  r.TotalRevenue,
		TotalExpenses: r.TotalExpenses,
		NetIncome:     r.NetIncome,
	}
	if r.From != nil {
		s := r.From.Format(time.RFC3339)
		resp.From = &s
	}
	if r.To != nil {
		s := r.To.Format(time.RFC3339)
		resp.To = &s
	}
	return resp
}

// handlePnLReport serves GET .../reports/pnl?from=&to= (both optional,
// RFC3339 timestamps - an omitted bound leaves that side of the period
// open, same convention as internal/auditlog's own ?from=/?to=).
func handlePnLReport(pool *pgxpool.Pool) http.HandlerFunc {
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
		from, to, err := parseReportDateRange(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		report, err := GetProfitAndLoss(r.Context(), pool, firmID, userID, from, to)
		if err != nil {
			writeAccountingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPnLReportResponse(report))
	}
}

// parseReportDateRange reads ?from=/?to= off r as optional RFC3339
// timestamps - shared by handlePnLReport's period bounds.
func parseReportDateRange(r *http.Request) (from, to *time.Time, err error) {
	q := r.URL.Query()
	if raw := q.Get("from"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, nil, errors.New("invalid from")
		}
		from = &t
	}
	if raw := q.Get("to"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, nil, errors.New("invalid to")
		}
		to = &t
	}
	return from, to, nil
}

type balanceSheetReportResponse struct {
	AsOf             string               `json:"asOf"`
	Assets           []reportLineResponse `json:"assets"`
	Liabilities      []reportLineResponse `json:"liabilities"`
	Equity           []reportLineResponse `json:"equity"`
	CurrentEarnings  string               `json:"currentEarnings"`
	TotalAssets      string               `json:"totalAssets"`
	TotalLiabilities string               `json:"totalLiabilities"`
	TotalEquity      string               `json:"totalEquity"`
	Balanced         bool                 `json:"balanced"`
	Difference       string               `json:"difference"`
}

func toBalanceSheetReportResponse(r BalanceSheetReport) balanceSheetReportResponse {
	return balanceSheetReportResponse{
		AsOf:             r.AsOf.Format(time.RFC3339),
		Assets:           toReportLineResponses(r.Assets),
		Liabilities:      toReportLineResponses(r.Liabilities),
		Equity:           toReportLineResponses(r.Equity),
		CurrentEarnings:  r.CurrentEarnings,
		TotalAssets:      r.TotalAssets,
		TotalLiabilities: r.TotalLiabilities,
		TotalEquity:      r.TotalEquity,
		Balanced:         r.Balanced,
		Difference:       r.Difference,
	}
}

// handleBalanceSheetReport serves GET .../reports/balance-sheet?as_of=
// (optional RFC3339 timestamp; defaults to now - a balance sheet always
// has a point in time, unlike the P&L's optional-both-bounds period).
func handleBalanceSheetReport(pool *pgxpool.Pool) http.HandlerFunc {
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
		asOf := time.Now()
		if raw := r.URL.Query().Get("as_of"); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				http.Error(w, "invalid as_of", http.StatusBadRequest)
				return
			}
			asOf = parsed
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		report, err := GetBalanceSheet(r.Context(), pool, firmID, userID, asOf)
		if err != nil {
			writeAccountingError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toBalanceSheetReportResponse(report))
	}
}
