// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package documents

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/contracts"
	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/invoicing"
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

// RegisterRoutes wires internal/documents' HTTP endpoints into mux, same
// bearer-token auth middleware and /api/firms/{firmID}/... convention as
// every other firm-scoped route group. The invoice render endpoint lives
// under invoicing's own path (/invoices/{invoiceID}/render) since it's
// invoice-specific, not a document-templates CRUD operation.
func RegisterRoutes(mux *http.ServeMux, verifier *identity.Verifier, pool *pgxpool.Pool) {
	auth := identity.Middleware(verifier)

	mux.Handle("GET /api/firms/{firmID}/document-templates", auth(http.HandlerFunc(handleListDocumentTemplates(pool))))
	mux.Handle("POST /api/firms/{firmID}/document-templates", auth(http.HandlerFunc(handleCreateDocumentTemplate(pool))))
	mux.Handle("GET /api/firms/{firmID}/document-templates/{id}", auth(http.HandlerFunc(handleGetDocumentTemplate(pool))))
	mux.Handle("PUT /api/firms/{firmID}/document-templates/{id}", auth(http.HandlerFunc(handleUpdateDocumentTemplate(pool))))

	mux.Handle("GET /api/firms/{firmID}/invoices/{invoiceID}/render", auth(http.HandlerFunc(handleRenderInvoice(pool))))
	// The contract render endpoint lives under contracts' own path
	// (/contracts/{contractID}/render), same "entity-specific render
	// lives under the entity's own path" convention the invoice route
	// above already establishes - no path collision with
	// internal/contracts.RegisterRoutes's own GET .../contracts/{contractID}
	// (a distinct pattern, the extra /render suffix), the same way this
	// package and internal/invoicing already coexist on sibling paths.
	mux.Handle("GET /api/firms/{firmID}/contracts/{contractID}/render", auth(http.HandlerFunc(handleRenderContract(pool))))
}

func writeDocumentsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFirmNotFound), errors.Is(err, ErrTemplateNotFound),
		errors.Is(err, invoicing.ErrFirmNotFound), errors.Is(err, invoicing.ErrInvoiceNotFound),
		errors.Is(err, contracts.ErrFirmNotFound), errors.Is(err, contracts.ErrContractNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrNotOwner):
		http.Error(w, err.Error(), http.StatusForbidden)
	case errors.Is(err, ErrInvalidTemplate), errors.Is(err, ErrTemplateTypeMismatch):
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

type documentTemplateResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Template  string `json:"template"`
	IsDefault bool   `json:"isDefault"`
	CreatedAt string `json:"createdAt"`
}

func toDocumentTemplateResponse(t DocumentTemplate) documentTemplateResponse {
	return documentTemplateResponse{
		ID: t.ID.String(), Name: t.Name, Type: string(t.Type), Template: t.Template,
		IsDefault: t.IsDefault, CreatedAt: t.CreatedAt.Format(time.RFC3339),
	}
}

type documentTemplateRequest struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Template  string `json:"template"`
	IsDefault bool   `json:"isDefault"`
}

func (req documentTemplateRequest) toInput() DocumentTemplateInput {
	return DocumentTemplateInput{Name: req.Name, Type: TemplateType(req.Type), Template: req.Template, IsDefault: req.IsDefault}
}

func handleListDocumentTemplates(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		templates, err := ListDocumentTemplates(r.Context(), pool, firmID, userID)
		if err != nil {
			writeDocumentsError(w, err)
			return
		}
		resp := make([]documentTemplateResponse, 0, len(templates))
		for _, t := range templates {
			resp = append(resp, toDocumentTemplateResponse(t))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func handleCreateDocumentTemplate(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		var req documentTemplateRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		tpl, err := CreateDocumentTemplate(r.Context(), pool, firmID, userID, req.toInput())
		if err != nil {
			writeDocumentsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(toDocumentTemplateResponse(tpl))
	}
}

func handleGetDocumentTemplate(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		templateID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid template id", http.StatusBadRequest)
			return
		}
		tpl, err := GetDocumentTemplate(r.Context(), pool, firmID, userID, templateID)
		if err != nil {
			writeDocumentsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toDocumentTemplateResponse(tpl))
	}
}

func handleUpdateDocumentTemplate(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firmID, userID, ok, status, msg := resolveIdentity(r, pool)
		if !ok {
			http.Error(w, msg, status)
			return
		}
		templateID, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			http.Error(w, "invalid template id", http.StatusBadRequest)
			return
		}
		var req documentTemplateRequest
		if err := decodeJSONBody(r, &req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		tpl, err := UpdateDocumentTemplate(r.Context(), pool, firmID, userID, templateID, req.toInput())
		if err != nil {
			writeDocumentsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(toDocumentTemplateResponse(tpl))
	}
}

// handleRenderInvoice serves GET /api/firms/{firmID}/invoices/{invoiceID}/render?templateID=
// - renders the invoice to HTML and returns it directly as
// Content-Type: text/html (not JSON) so a browser tab opened on this URL
// (the invoice detail page's own "Preview" button) renders the document,
// same "direct browser navigation" convention as
// internal/portability.handleExport's Content-Disposition use.
func handleRenderInvoice(pool *pgxpool.Pool) http.HandlerFunc {
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
		var templateID uuid.UUID
		if raw := r.URL.Query().Get("templateID"); raw != "" {
			templateID, err = uuid.Parse(raw)
			if err != nil {
				http.Error(w, "invalid template id", http.StatusBadRequest)
				return
			}
		}

		html, err := RenderInvoice(r.Context(), pool, firmID, userID, invoiceID, templateID)
		if err != nil {
			writeDocumentsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// #nosec G705 -- this IS the feature: html renders a firm-owned,
		// owner-authored document_templates.template body through
		// html/template (RenderInvoice), which auto-escapes every dynamic
		// {{.Field}} substitution - only the template author's own static
		// markup is unescaped raw HTML, by design, the same way any
		// template-authoring tool (an email builder, a CMS) treats its
		// own authors' markup as trusted output, not untrusted input.
		_, _ = w.Write([]byte(html))
	}
}

// handleRenderContract serves GET /api/firms/{firmID}/contracts/{contractID}/render?templateID=
// - same shape as handleRenderInvoice above.
func handleRenderContract(pool *pgxpool.Pool) http.HandlerFunc {
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
		var templateID uuid.UUID
		if raw := r.URL.Query().Get("templateID"); raw != "" {
			templateID, err = uuid.Parse(raw)
			if err != nil {
				http.Error(w, "invalid template id", http.StatusBadRequest)
				return
			}
		}

		html, err := RenderContract(r.Context(), pool, firmID, userID, contractID, templateID)
		if err != nil {
			writeDocumentsError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// #nosec G705 -- same reasoning handleRenderInvoice's own comment
		// above gives: this IS the feature, auto-escaped by html/template.
		_, _ = w.Write([]byte(html))
	}
}
