// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package auditlog

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

// maxListLimit caps the ?limit= query param on GET .../audit-log, same
// reasoning and same numeric cap as internal/workflow's
// maxListInstancesLimit: bounded enough that a single page never becomes
// an unbounded full-table fetch, generous enough that no real UI page
// size needs more than one request.
const maxListLimit = 200

// RegisterRoutes wires the audit log's one HTTP endpoint into mux, mirroring
// every other firm-scoped route group's /api/firms/{firmID}/... convention
// and bearer-token auth middleware.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)
	mux.Handle("GET /api/firms/{firmID}/audit-log", auth(http.HandlerFunc(handleList(pool))))
}

// writeError maps this package's sentinel errors to the HTTP status that
// reflects why the caller isn't getting what they asked for - same
// convention as internal/workflow's writeEngineError and
// internal/permission's writeAuditError: 404 for anything not visible to
// the caller at all (including non-membership), 403 for a real
// authorization denial.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrPermissionDenied):
		http.Error(w, err.Error(), http.StatusForbidden)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type entryResponse struct {
	ID              string         `json:"id"`
	EntityType      string         `json:"entityType"`
	EntityID        string         `json:"entityId"`
	Action          string         `json:"action"`
	Changes         map[string]any `json:"changes"`
	UserID          string         `json:"userId"`
	UserEmail       string         `json:"userEmail"`
	UserDisplayName string         `json:"userDisplayName"`
	OccurredAt      string         `json:"occurredAt"`
}

// handleList lists firmID's audit_log entries, most recent first.
//
// Optional query params: ?limit= and ?offset= for paging (both default
// to "off" - a request with neither returns every entry, unpaged, same
// response shape as before this endpoint supported paging - see
// ListOptions's zero value, same convention as
// internal/workflow's handleListInstances); ?from=/?to= (RFC3339
// timestamps) for an inclusive date-range filter on occurred_at; and
// ?definitionId= to keep only entries that resolve back to that workflow
// definition (see List's doc comment for exactly which entries that
// matches). The response body stays a plain array either way, so no
// existing caller breaks; the total matching count (before paging) is
// reported via the X-Total-Count response header instead of changing the
// body shape - Never-Violate Rule 6.
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

		result, err := List(r.Context(), pool, firmID, userID, opts)
		if err != nil {
			writeError(w, err)
			return
		}

		resp := make([]entryResponse, 0, len(result.Entries))
		for _, e := range result.Entries {
			resp = append(resp, entryResponse{
				ID:              e.ID.String(),
				EntityType:      e.EntityType,
				EntityID:        e.EntityID.String(),
				Action:          e.Action,
				Changes:         e.Changes,
				UserID:          e.UserID.String(),
				UserEmail:       e.UserEmail,
				UserDisplayName: e.UserDisplayName,
				OccurredAt:      e.OccurredAt.Format(time.RFC3339),
			})
		}

		w.Header().Set("X-Total-Count", strconv.Itoa(result.Total))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// parseListOptions reads ?limit=/?offset=/?from=/?to=/?definitionId= off
// r into ListOptions, rejecting a negative or over-cap limit/offset, an
// unparseable timestamp, or an unparseable definition id outright rather
// than silently ignoring it - a caller sending garbage should see a 400,
// not a quietly "corrected" or dropped filter. Mirrors
// internal/workflow's parseListInstancesOptions.
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
	if raw := q.Get("from"); raw != "" {
		from, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return ListOptions{}, errors.New("invalid from")
		}
		opts.From = &from
	}
	if raw := q.Get("to"); raw != "" {
		to, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return ListOptions{}, errors.New("invalid to")
		}
		opts.To = &to
	}
	if raw := q.Get("definitionId"); raw != "" {
		definitionID, err := uuid.Parse(raw)
		if err != nil {
			return ListOptions{}, errors.New("invalid definitionId")
		}
		opts.DefinitionID = &definitionID
	}

	return opts, nil
}
