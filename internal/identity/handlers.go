// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package identity

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterRoutes wires the identity-facing HTTP endpoints into mux, both
// protected by verifier's bearer-token check. This is the only place in
// the server where a raw Keycloak token turns into ZonaryOS's own
// user/firm/role model.
func RegisterRoutes(mux *http.ServeMux, verifier *Verifier, pool *pgxpool.Pool) {
	auth := Middleware(verifier)

	mux.Handle("GET /api/me", auth(http.HandlerFunc(handleMe(pool))))
	mux.Handle("GET /api/me/firms/{firmID}/role", auth(http.HandlerFunc(handleRoleInFirm(pool))))
	mux.Handle("GET /api/me/preferences", auth(http.HandlerFunc(handleGetPreferences(pool))))
	mux.Handle("PATCH /api/me/preferences", auth(http.HandlerFunc(handlePatchPreferences(pool))))
}

type meFirmResponse struct {
	FirmID   string `json:"firmId"`
	FirmName string `json:"firmName"`
	RoleID   string `json:"roleId"`
}

type meResponse struct {
	Subject     string           `json:"subject"`
	Email       string           `json:"email"`
	DisplayName string           `json:"displayName"`
	Firms       []meFirmResponse `json:"firms"`
}

// handleMe resolves the caller's identity into a ZonaryOS user record and
// lists every firm they belong to - the first half of "wire the
// authenticated identity into the firm/role model" (the second half,
// opening an RLS-scoped transaction for one chosen firm, is
// handleRoleInFirm).
func handleMe(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}

		userID, err := ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		memberships, err := Memberships(r.Context(), pool, userID)
		if err != nil {
			http.Error(w, "failed to list firm memberships", http.StatusInternalServerError)
			return
		}

		resp := meResponse{
			Subject:     id.Subject,
			Email:       id.Email,
			DisplayName: id.DisplayName,
			Firms:       make([]meFirmResponse, 0, len(memberships)),
		}
		for _, m := range memberships {
			resp.Firms = append(resp.Firms, meFirmResponse{
				FirmID:   m.FirmID.String(),
				FirmName: m.FirmName,
				RoleID:   m.RoleID.String(),
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

type roleInFirmResponse struct {
	FirmID   string `json:"firmId"`
	RoleID   string `json:"roleId"`
	RoleKey  string `json:"roleKey"`
	RoleName string `json:"roleName"`
	IsOwner  bool   `json:"isOwner"`
}

// handleRoleInFirm opens a firm-scoped, RLS-enforced transaction (via
// RoleInFirm -> db.WithFirmContext) for the caller's chosen firm. A caller
// with no membership in that firm gets a 403 - not because application
// code checked a list, but because RLS itself has nothing to return.
func handleRoleInFirm(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}

		firmID, err := uuid.Parse(r.PathValue("firmID"))
		if err != nil {
			http.Error(w, "invalid firm id", http.StatusBadRequest)
			return
		}

		userID, err := ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		detail, err := RoleInFirm(r.Context(), pool, firmID, userID)
		if err != nil {
			http.Error(w, "no membership in this firm", http.StatusForbidden)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(roleInFirmResponse{
			FirmID:   firmID.String(),
			RoleID:   detail.RoleID.String(),
			RoleKey:  detail.RoleKey,
			RoleName: detail.RoleName,
			IsOwner:  detail.IsOwner,
		})
	}
}

type preferencesResponse struct {
	Theme         *string `json:"theme,omitempty"`
	Density       *string `json:"density,omitempty"`
	DefaultLocale *string `json:"defaultLocale,omitempty"`
}

func toPreferencesResponse(p Preferences) preferencesResponse {
	return preferencesResponse{Theme: p.Theme, Density: p.Density, DefaultLocale: p.DefaultLocale}
}

// handleGetPreferences implements GET /api/me/preferences - gated only
// by identity.Middleware's bearer-token check (unauthenticated-blocked,
// per the design brief), not by any firm membership: a user has
// preferences regardless of which firm they're currently viewing, the
// same "no firm context at all" shape handleMe itself already has.
func handleGetPreferences(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		userID, err := ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		prefs, err := GetPreferences(r.Context(), pool, userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPreferencesResponse(prefs))
	}
}

// handlePatchPreferences implements PATCH /api/me/preferences - a
// partial update, same semantics as PatchPreferences itself.
func handlePatchPreferences(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}
		userID, err := ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		var patch Preferences
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
		}

		prefs, err := PatchPreferences(r.Context(), pool, userID, patch)
		if err != nil {
			if errors.Is(err, ErrInvalidPreferences) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toPreferencesResponse(prefs))
	}
}
