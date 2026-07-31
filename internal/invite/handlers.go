// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package invite

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// RegisterRoutes wires this package's HTTP endpoints into mux. Two
// different auth postures on purpose:
//   - The firm-scoped management endpoints (generate/list/revoke) sit
//     behind the same bearer-token `auth` middleware and
//     /api/firms/{firmID}/... convention as every other owner-gated route
//     group (see internal/firm.RegisterRoutes, internal/permission's role
//     endpoints).
//   - GET /api/invites/{token} is genuinely public - no bearer token at
//     all - because the whole point is to let an unauthenticated visitor
//     see what they were invited to before they sign in (see this
//     package's doc comment and Lookup).
//   - POST /api/invites/{token}/accept requires *a* valid bearer token
//     (some authenticated ZonaryOS user must exist to grant the role to),
//     but deliberately not firm-scoped: it is the one route in this
//     codebase that has to work for a caller with no membership in the
//     target firm at all - see Accept's ciaudit:ignore-firmid-check note.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("POST /api/firms/{firmID}/invites", auth(http.HandlerFunc(handleGenerate(pool))))
	mux.Handle("GET /api/firms/{firmID}/invites", auth(http.HandlerFunc(handleList(pool))))
	mux.Handle("POST /api/firms/{firmID}/invites/{inviteID}/revoke", auth(http.HandlerFunc(handleRevoke(pool))))

	mux.Handle("GET /api/invites/{token}", http.HandlerFunc(handleLookup(pool)))
	mux.Handle("POST /api/invites/{token}/accept", auth(http.HandlerFunc(handleAccept(pool))))
}

// writeError maps this package's sentinel errors to the HTTP status that
// reflects why the caller isn't getting what they asked for - same
// convention as internal/firm's writeError. Expired/used/revoked each get
// a distinct 4xx (not all folded into one generic "invalid invite")
// specifically so the frontend can show the distinct message the brief
// calls for, without needing to parse the error body text.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrPermissionDenied):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrRoleNotFound):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrInviteNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrInviteExpired):
		http.Error(w, err.Error(), http.StatusGone)
	case errors.Is(err, ErrInviteUsed), errors.Is(err, ErrInviteRevoked), errors.Is(err, ErrInviteNotPending):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type inviteResponse struct {
	InviteID  string  `json:"inviteId"`
	RoleID    string  `json:"roleId"`
	RoleName  string  `json:"roleName"`
	Token     *string `json:"token,omitempty"`
	Status    string  `json:"status"`
	Expired   bool    `json:"expired"`
	ExpiresAt string  `json:"expiresAt"`
	CreatedAt string  `json:"createdAt"`
	UsedAt    *string `json:"usedAt,omitempty"`
}

func toInviteResponse(inv Invite, includeToken bool) inviteResponse {
	resp := inviteResponse{
		InviteID:  inv.ID.String(),
		RoleID:    inv.RoleID.String(),
		RoleName:  inv.RoleName,
		Status:    inv.Status,
		Expired:   inv.Expired,
		ExpiresAt: inv.ExpiresAt.Format(timeFormat),
		CreatedAt: inv.CreatedAt.Format(timeFormat),
	}
	if includeToken {
		token := inv.Token
		resp.Token = &token
	}
	if inv.UsedAt != nil {
		usedAt := inv.UsedAt.Format(timeFormat)
		resp.UsedAt = &usedAt
	}
	return resp
}

const timeFormat = "2006-01-02T15:04:05Z07:00"

type generateRequest struct {
	RoleID string `json:"roleId"`
}

// handleGenerate is POST /api/firms/{firmID}/invites - owner-gated invite
// creation. The token is only ever returned here, at creation time
// (handleList below never re-serializes it) - the owner is expected to
// copy the link this response builds into out of the UI immediately;
// nothing persists it back out to the frontend after that first response.
func handleGenerate(pool *pgxpool.Pool) http.HandlerFunc {
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
		var req generateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		roleID, err := uuid.Parse(req.RoleID)
		if err != nil {
			http.Error(w, "invalid role id", http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		inv, err := Generate(r.Context(), pool, firmID, userID, roleID)
		if err != nil {
			writeError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toInviteResponse(inv, true))
	}
}

// handleList is GET /api/firms/{firmID}/invites - owner-gated, the
// invites-management table's data source (never includes the token
// itself - see toInviteResponse).
func handleList(pool *pgxpool.Pool) http.HandlerFunc {
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

		invites, err := List(r.Context(), pool, firmID, userID)
		if err != nil {
			writeError(w, err)
			return
		}

		resp := make([]inviteResponse, 0, len(invites))
		for _, inv := range invites {
			resp = append(resp, toInviteResponse(inv, false))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// handleRevoke is POST /api/firms/{firmID}/invites/{inviteID}/revoke -
// owner-gated. POST rather than DELETE (matching internal/workflow's
// action-endpoint convention, e.g. .../transitions/{actionKey}) since this
// is a status transition on a row that is never removed, not a deletion.
func handleRevoke(pool *pgxpool.Pool) http.HandlerFunc {
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
		inviteID, err := uuid.Parse(r.PathValue("inviteID"))
		if err != nil {
			http.Error(w, "invalid invite id", http.StatusBadRequest)
			return
		}
		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		if err := Revoke(r.Context(), pool, firmID, userID, inviteID); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type lookupResponse struct {
	FirmName  string `json:"firmName"`
	RoleName  string `json:"roleName"`
	Status    string `json:"status"`
	Expired   bool   `json:"expired"`
	ExpiresAt string `json:"expiresAt"`
}

// handleLookup is GET /api/invites/{token} - genuinely public, no bearer
// token required (see RegisterRoutes's doc comment). A non-existent token
// gets a distinct 404 from an expired/used/revoked one (410/409 via
// writeError), matching the brief's requirement for distinct, correct
// messaging per state rather than one generic error.
func handleLookup(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		if token == "" {
			http.Error(w, "invalid invite token", http.StatusBadRequest)
			return
		}

		info, err := Lookup(r.Context(), pool, token)
		if err != nil {
			writeError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(lookupResponse{
			FirmName:  info.FirmName,
			RoleName:  info.RoleName,
			Status:    info.Status,
			Expired:   info.Expired,
			ExpiresAt: info.ExpiresAt.Format(timeFormat),
		})
	}
}

type acceptResponse struct {
	FirmID   string `json:"firmId"`
	RoleID   string `json:"roleId"`
	RoleName string `json:"roleName"`
}

// handleAccept is POST /api/invites/{token}/accept - requires the caller
// to already be authenticated (an actual Keycloak-backed identity to
// grant the role to) but not a member of any particular firm - see
// RegisterRoutes's doc comment and Accept's ciaudit:ignore-firmid-check
// note for why that's correct here, not an authorization gap.
func handleAccept(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		token := r.PathValue("token")
		if token == "" {
			http.Error(w, "invalid invite token", http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		inv, err := Accept(r.Context(), pool, token, userID)
		if err != nil {
			writeError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(acceptResponse{
			FirmID:   inv.FirmID.String(),
			RoleID:   inv.RoleID.String(),
			RoleName: inv.RoleName,
		})
	}
}
