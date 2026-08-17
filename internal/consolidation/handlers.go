// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package consolidation

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
)

// RegisterRoutes wires this package's HTTP endpoints into mux. Every
// /api/platform-admin/firm-groups/... route requires allow.Contains
// (same Allowlist internal/platformadmin/internal/currency's own
// platform-admin-only writes already reuse) - GET
// /api/firms/{firmID}/group is the one firm-scoped, member-gated
// exception (see GetFirmGroupForFirm's own doc comment).
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool, allow platformadmin.Allowlist) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/platform-admin/firm-groups", auth(http.HandlerFunc(handleListFirmGroups(pool, allow))))
	mux.Handle("POST /api/platform-admin/firm-groups", auth(http.HandlerFunc(handleCreateFirmGroup(pool, allow))))
	mux.Handle("POST /api/platform-admin/firm-groups/{groupID}/members", auth(http.HandlerFunc(handleAddGroupMember(pool, allow))))
	mux.Handle("POST /api/platform-admin/firm-groups/{groupID}/intercompany-transfer", auth(http.HandlerFunc(handleIntercompanyTransfer(pool, allow))))
	mux.Handle("GET /api/platform-admin/firm-groups/{groupID}/consolidated-pnl", auth(http.HandlerFunc(handleConsolidatedPnL(pool, allow))))

	mux.Handle("GET /api/firms/{firmID}/group", auth(http.HandlerFunc(handleGetFirmGroupForFirm(pool))))
}

func decodeJSONBody(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// writeConsolidationError maps this package's sentinels to HTTP status
// codes. ErrNotPlatformAdmin -> 404 (not 403), same "off-allowlist looks
// like the route doesn't exist" posture
// internal/platformadmin.handleListFirms/internal/currency.handleCreateRate
// already establish.
func writeConsolidationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platformadmin.ErrNotPlatformAdmin):
		http.Error(w, "404 page not found", http.StatusNotFound)
	case errors.Is(err, ErrGroupNotFound), errors.Is(err, ErrFirmNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrInvalidGroup), errors.Is(err, ErrInvalidTransfer),
		errors.Is(err, ErrFirmNotInGroup), errors.Is(err, ErrDuplicateMembership):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// requireIdentity reads the already-verified caller identity off the
// request context - the platform-admin allowlist check itself happens
// inside each service-layer function (CreateFirmGroup et al.), same
// division of responsibility internal/currency.handleCreateRate uses.
func requireIdentity(r *http.Request) (identity.Identity, bool) {
	return identity.FromContext(r.Context())
}

func parseDateRange(r *http.Request) (from, to *time.Time, err error) {
	if raw := r.URL.Query().Get("from"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, nil, errors.New("invalid from (expected RFC3339)")
		}
		from = &t
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, nil, errors.New("invalid to (expected RFC3339)")
		}
		to = &t
	}
	return from, to, nil
}

type firmGroupMemberResponse struct {
	FirmID       string  `json:"firmId"`
	FirmName     string  `json:"firmName"`
	Role         string  `json:"role"`
	OwnershipPct *string `json:"ownershipPct,omitempty"`
}

type firmGroupResponse struct {
	ID        string                    `json:"id"`
	Name      string                    `json:"name"`
	CreatedAt string                    `json:"createdAt"`
	Members   []firmGroupMemberResponse `json:"members"`
}

func toFirmGroupResponse(g FirmGroupDetail) firmGroupResponse {
	members := make([]firmGroupMemberResponse, 0, len(g.Members))
	for _, m := range g.Members {
		members = append(members, firmGroupMemberResponse{
			FirmID: m.FirmID.String(), FirmName: m.FirmName, Role: string(m.Role), OwnershipPct: m.OwnershipPct,
		})
	}
	return firmGroupResponse{ID: g.ID.String(), Name: g.Name, CreatedAt: g.CreatedAt.Format(time.RFC3339), Members: members}
}

func handleListFirmGroups(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireIdentity(r)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		if !allow.Contains(id.Email) {
			http.NotFound(w, r)
			return
		}

		groups, err := ListFirmGroups(r.Context(), pool, allow, id)
		if err != nil {
			writeConsolidationError(w, err)
			return
		}
		resp := make([]firmGroupResponse, 0, len(groups))
		for _, g := range groups {
			resp = append(resp, toFirmGroupResponse(g))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type createFirmGroupRequest struct {
	Name string `json:"name"`
}

func handleCreateFirmGroup(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireIdentity(r)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		if !allow.Contains(id.Email) {
			http.NotFound(w, r)
			return
		}

		var req createFirmGroupRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		g, err := CreateFirmGroup(r.Context(), pool, allow, id, req.Name)
		if err != nil {
			writeConsolidationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toFirmGroupResponse(FirmGroupDetail{FirmGroup: g, Members: []FirmGroupMemberView{}}))
	}
}

type addGroupMemberRequest struct {
	FirmID       string  `json:"firmId"`
	Role         string  `json:"role"`
	OwnershipPct *string `json:"ownershipPct"`
}

type firmGroupMemberWriteResponse struct {
	ID           string  `json:"id"`
	GroupID      string  `json:"groupId"`
	FirmID       string  `json:"firmId"`
	Role         string  `json:"role"`
	OwnershipPct *string `json:"ownershipPct,omitempty"`
	JoinedAt     string  `json:"joinedAt"`
}

func handleAddGroupMember(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireIdentity(r)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		if !allow.Contains(id.Email) {
			http.NotFound(w, r)
			return
		}
		groupID, err := uuid.Parse(r.PathValue("groupID"))
		if err != nil {
			http.Error(w, "invalid group id", http.StatusBadRequest)
			return
		}

		var req addGroupMemberRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		firmID, err := uuid.Parse(req.FirmID)
		if err != nil {
			http.Error(w, "invalid firmId", http.StatusBadRequest)
			return
		}

		m, err := AddGroupMember(r.Context(), pool, allow, id, groupID, firmID, FirmGroupRole(req.Role), req.OwnershipPct)
		if err != nil {
			writeConsolidationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(firmGroupMemberWriteResponse{
			ID: m.ID.String(), GroupID: m.GroupID.String(), FirmID: m.FirmID.String(),
			Role: string(m.Role), OwnershipPct: m.OwnershipPct, JoinedAt: m.JoinedAt.Format(time.RFC3339),
		})
	}
}

type intercompanyTransferRequest struct {
	FromFirmID  string `json:"fromFirmId"`
	ToFirmID    string `json:"toFirmId"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	Description string `json:"description"`
}

type intercompanyTransactionResponse struct {
	ID                 string  `json:"id"`
	GroupID            string  `json:"groupId"`
	FromFirmID         string  `json:"fromFirmId"`
	ToFirmID           string  `json:"toFirmId"`
	Amount             string  `json:"amount"`
	Currency           string  `json:"currency"`
	Description        string  `json:"description"`
	FromJournalEntryID *string `json:"fromJournalEntryId,omitempty"`
	ToJournalEntryID   *string `json:"toJournalEntryId,omitempty"`
	CreatedAt          string  `json:"createdAt"`
}

func handleIntercompanyTransfer(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireIdentity(r)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		if !allow.Contains(id.Email) {
			http.NotFound(w, r)
			return
		}
		groupID, err := uuid.Parse(r.PathValue("groupID"))
		if err != nil {
			http.Error(w, "invalid group id", http.StatusBadRequest)
			return
		}

		var req intercompanyTransferRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		fromFirmID, err := uuid.Parse(req.FromFirmID)
		if err != nil {
			http.Error(w, "invalid fromFirmId", http.StatusBadRequest)
			return
		}
		toFirmID, err := uuid.Parse(req.ToFirmID)
		if err != nil {
			http.Error(w, "invalid toFirmId", http.StatusBadRequest)
			return
		}

		txn, err := PostIntercompanyTransfer(r.Context(), pool, allow, id, groupID, fromFirmID, toFirmID, req.Amount, req.Currency, req.Description)
		if err != nil {
			writeConsolidationError(w, err)
			return
		}

		resp := intercompanyTransactionResponse{
			ID: txn.ID.String(), GroupID: txn.GroupID.String(), FromFirmID: txn.FromFirmID.String(), ToFirmID: txn.ToFirmID.String(),
			Amount: txn.Amount, Currency: txn.Currency, Description: txn.Description, CreatedAt: txn.CreatedAt.Format(time.RFC3339),
		}
		if txn.FromJournalEntryID != nil {
			s := txn.FromJournalEntryID.String()
			resp.FromJournalEntryID = &s
		}
		if txn.ToJournalEntryID != nil {
			s := txn.ToJournalEntryID.String()
			resp.ToJournalEntryID = &s
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type consolidatedPnLFirmLineResponse struct {
	FirmID    string `json:"firmId"`
	FirmName  string `json:"firmName"`
	Revenue   string `json:"revenue"`
	Expenses  string `json:"expenses"`
	NetIncome string `json:"netIncome"`
}

type consolidatedPnLResponse struct {
	GroupID       string                            `json:"groupId"`
	From          *string                           `json:"from,omitempty"`
	To            *string                           `json:"to,omitempty"`
	Firms         []consolidatedPnLFirmLineResponse `json:"firms"`
	TotalRevenue  string                            `json:"totalRevenue"`
	TotalExpenses string                            `json:"totalExpenses"`
	NetIncome     string                            `json:"netIncome"`
}

func handleConsolidatedPnL(pool *pgxpool.Pool, allow platformadmin.Allowlist) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireIdentity(r)
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		if !allow.Contains(id.Email) {
			http.NotFound(w, r)
			return
		}
		groupID, err := uuid.Parse(r.PathValue("groupID"))
		if err != nil {
			http.Error(w, "invalid group id", http.StatusBadRequest)
			return
		}
		from, to, err := parseDateRange(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		report, err := GetConsolidatedPnL(r.Context(), pool, allow, id, groupID, from, to)
		if err != nil {
			writeConsolidationError(w, err)
			return
		}

		resp := consolidatedPnLResponse{
			GroupID: report.GroupID.String(), TotalRevenue: report.TotalRevenue, TotalExpenses: report.TotalExpenses, NetIncome: report.NetIncome,
			Firms: make([]consolidatedPnLFirmLineResponse, 0, len(report.Firms)),
		}
		if report.From != nil {
			s := report.From.Format(time.RFC3339)
			resp.From = &s
		}
		if report.To != nil {
			s := report.To.Format(time.RFC3339)
			resp.To = &s
		}
		for _, f := range report.Firms {
			resp.Firms = append(resp.Firms, consolidatedPnLFirmLineResponse{
				FirmID: f.FirmID.String(), FirmName: f.FirmName, Revenue: f.Revenue, Expenses: f.Expenses, NetIncome: f.NetIncome,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleGetFirmGroupForFirm(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := requireIdentity(r)
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

		detail, err := GetFirmGroupForFirm(r.Context(), pool, firmID, userID)
		if err != nil {
			writeConsolidationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if detail == nil {
			// Not being in any group is not an error - a 200 with a null
			// body lets the frontend distinguish "no group" from a 4xx/5xx
			// failure cleanly, same convention a nullable *string JSON
			// field elsewhere in this codebase (e.g. costCenterId) uses at
			// the value level, just applied to the whole response here.
			_ = json.NewEncoder(w).Encode(nil)
			return
		}
		_ = json.NewEncoder(w).Encode(toFirmGroupResponse(*detail))
	}
}
