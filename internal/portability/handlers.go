// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package portability

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
)

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

// RegisterRoutes wires portability's HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/export", auth(http.HandlerFunc(handleExport(pool))))
	mux.Handle("POST /api/firms/{firmID}/import/configuration", auth(http.HandlerFunc(handleImportConfiguration(pool))))
}

func writePortabilityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidExportDocument):
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

// exportFormat is handleExport's own format-negotiation result - "json"
// (the existing, default behavior) or "csv" (this batch's addition).
// Future formats (xlsx, pdf - see docs/DEVELOPMENT.md's own note on this)
// are NOT handled here yet; requesting one falls back to "json" today,
// same as any other unrecognized format value.
func exportFormat(r *http.Request) string {
	if q := r.URL.Query().Get("format"); q != "" {
		return q
	}
	accept := r.Header.Get("Accept")
	if accept == "text/csv" {
		return "csv"
	}
	return "json"
}

// handleExport serves GET /api/firms/{firmID}/export - the full-firm
// snapshot, owner-only. Defaults to JSON (Content-Disposition: attachment
// so a direct browser navigation, the /settings "Export" button,
// downloads a file rather than rendering raw JSON inline); ?format=csv
// (or an `Accept: text/csv` request) instead returns a multi-section CSV
// (internal/portability.BuildCSV) - read-only, no CSV import path (see
// this package's doc comment on scope).
func handleExport(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		doc, err := ExportFirm(r.Context(), pool, firmID, userID)
		if err != nil {
			writePortabilityError(w, err)
			return
		}

		if exportFormat(r) == "csv" {
			body, err := BuildCSV(doc)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Content-Disposition", `attachment; filename="zonaryos-export.csv"`)
			_, _ = w.Write(body)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="zonaryos-export.json"`)
		_ = json.NewEncoder(w).Encode(doc)
	}
}

// handleImportConfiguration serves POST /api/firms/{firmID}/import/configuration -
// the request body is a full ExportDocument (typically one downloaded via
// handleExport, possibly from a DIFFERENT ZonaryOS instance entirely -
// Vision §3's "avoids vendor lock-in"); only its configuration fields are
// read (see ImportConfiguration's own doc comment).
func handleImportConfiguration(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}

		var doc ExportDocument
		if err := decodeJSONBody(r, &doc); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		summary, err := ImportConfiguration(r.Context(), pool, firmID, userID, doc)
		if err != nil {
			writePortabilityError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(summary)
	}
}
