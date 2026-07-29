package auditlog

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

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

		entries, err := List(r.Context(), pool, firmID, userID)
		if err != nil {
			writeError(w, err)
			return
		}

		resp := make([]entryResponse, 0, len(entries))
		for _, e := range entries {
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

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}
