// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package permission

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// sseKeepAliveInterval is how often handleEvents writes a comment-only
// SSE line to keep the connection alive through proxies/load balancers
// that time out idle connections - it carries no event, existing purely
// so the TCP connection looks active.
const sseKeepAliveInterval = 25 * time.Second

// RegisterRoutes wires Permission Audit Mode's HTTP endpoints into mux,
// protected by verifier's bearer-token check like every other route
// group. Firm-scoped, mirroring internal/workflow's
// /api/firms/{firmID}/... path convention. broadcaster is shared across
// the whole server process (see cmd/server/main.go): grant/revoke publish
// to it, the SSE endpoint subscribes from it.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool, broadcaster *Broadcaster) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/permission-audit", auth(http.HandlerFunc(handleGetAudit(pool))))
	mux.Handle("PUT /api/firms/{firmID}/roles/{roleID}/permissions/{key}", auth(http.HandlerFunc(handleGrant(pool, broadcaster))))
	mux.Handle("DELETE /api/firms/{firmID}/roles/{roleID}/permissions/{key}", auth(http.HandlerFunc(handleRevoke(pool, broadcaster))))
	mux.Handle("GET /api/firms/{firmID}/permission-events", auth(http.HandlerFunc(handleEvents(pool, broadcaster))))
}

// writeAuditError maps this package's sentinel errors to the HTTP status
// that reflects why the caller isn't getting what they asked for - same
// convention as internal/workflow's writeEngineError: 404 for anything
// not visible to the caller at all (including non-membership - never
// distinguishable from "doesn't exist"), 403 for a real authorization
// denial, 400 for a structurally invalid request.
func writeAuditError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrRoleNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrOwnerRoleImmutable), errors.Is(err, ErrPermissionKeyNotFound):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type roleGrantResponse struct {
	RoleID         string   `json:"roleId"`
	RoleKey        string   `json:"roleKey"`
	RoleName       string   `json:"roleName"`
	PermissionKeys []string `json:"permissionKeys"`
}

type firmPermissionAuditResponse struct {
	MyPermissionKeys []string            `json:"myPermissionKeys"`
	Roles            []roleGrantResponse `json:"roles"`
}

func handleGetAudit(pool *pgxpool.Pool) http.HandlerFunc {
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

		audit, err := GetFirmPermissionAudit(r.Context(), pool, firmID, userID)
		if err != nil {
			writeAuditError(w, err)
			return
		}

		resp := firmPermissionAuditResponse{
			MyPermissionKeys: audit.MyPermissionKeys,
			Roles:            make([]roleGrantResponse, 0, len(audit.Roles)),
		}
		for _, g := range audit.Roles {
			resp.Roles = append(resp.Roles, roleGrantResponse{
				RoleID:         g.RoleID.String(),
				RoleKey:        g.RoleKey,
				RoleName:       g.RoleName,
				PermissionKeys: g.PermissionKeys,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// parseGrantRequest extracts and validates the three path values every
// grant/revoke handler needs.
func parseGrantRequest(r *http.Request) (firmID, roleID uuid.UUID, permissionKey string, err error) {
	firmID, err = uuid.Parse(r.PathValue("firmID"))
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, "", fmt.Errorf("invalid firm id")
	}
	roleID, err = uuid.Parse(r.PathValue("roleID"))
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, "", fmt.Errorf("invalid role id")
	}
	permissionKey = r.PathValue("key")
	if permissionKey == "" {
		return uuid.UUID{}, uuid.UUID{}, "", fmt.Errorf("missing permission key")
	}
	return firmID, roleID, permissionKey, nil
}

func handleGrant(pool *pgxpool.Pool, broadcaster *Broadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}

		firmID, roleID, permissionKey, err := parseGrantRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		if err := GrantPermission(r.Context(), pool, firmID, userID, roleID, permissionKey); err != nil {
			writeAuditError(w, err)
			return
		}

		// Vision §3: "the change is saved immediately, badges ... refresh
		// automatically, active sessions for the affected role are
		// reconnected." Publish after the transaction commits, not before -
		// a subscriber that wakes up and re-fetches must see the new grant.
		broadcaster.Publish(firmID)

		w.WriteHeader(http.StatusNoContent)
	}
}

func handleRevoke(pool *pgxpool.Pool, broadcaster *Broadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identity.FromContext(r.Context())
		if !ok {
			http.Error(w, "missing identity", http.StatusInternalServerError)
			return
		}

		firmID, roleID, permissionKey, err := parseGrantRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		userID, err := identity.ResolveOrCreateUser(r.Context(), pool, id)
		if err != nil {
			http.Error(w, "failed to resolve user", http.StatusInternalServerError)
			return
		}

		if err := RevokePermission(r.Context(), pool, firmID, userID, roleID, permissionKey); err != nil {
			writeAuditError(w, err)
			return
		}

		broadcaster.Publish(firmID)

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleEvents is a Server-Sent Events stream: one "permissions-changed"
// event per Broadcaster.Publish(firmID) call, for as long as the client
// stays connected. Open to any firm member (IsMember, not IsOwner-gated
// like the audit/grant/revoke endpoints above) - Vision §3's "active
// sessions for the affected role are reconnected" means the affected
// role's own sessions, not just an owner watching Audit Mode, so any
// authenticated member of the firm may subscribe.
func handleEvents(pool *pgxpool.Pool, broadcaster *Broadcaster) http.HandlerFunc {
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

		// Membership-checked before the stream opens, same discipline as
		// every other endpoint touching firm-scoped data (docs/
		// DEVELOPMENT.md's Authorization checklist) - a long-lived
		// connection is not an exemption from it.
		member, err := CheckMembership(r.Context(), pool, firmID, userID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !member {
			http.Error(w, ErrFirmNotFound.Error(), http.StatusNotFound)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		ch, unsubscribe := broadcaster.Subscribe(firmID)
		defer unsubscribe()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		ticker := time.NewTicker(sseKeepAliveInterval)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ch:
				if _, err := fmt.Fprint(w, "event: permissions-changed\ndata: {}\n\n"); err != nil {
					return
				}
				flusher.Flush()
			case <-ticker.C:
				if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}
