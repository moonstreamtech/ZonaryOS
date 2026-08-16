// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package documents is internal/invoicing's own documented "no PDF
// generation (invoices as structured data first)" scope boundary's
// natural next step: firm-defined document templates
// (Handlebars/Mustache-*looking* {{field}} syntax) rendered to HTML via
// Go's stdlib html/template - no new dependency, no PDF output. A
// template's data is always passed as a nested map[string]any (never a
// Go struct directly) with template.Option("missingkey=zero") set - see
// render.go's own doc comment for why: a template referencing a field
// that doesn't exist on the entity must render an empty string, not
// fail the whole render.
//
// Authorization follows this codebase's usual discipline: IsMember
// before any read, IsOwner before any direct write - the same tier as
// internal/localization's address/tax-rate catalog mutations.
package documents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - template writes are owner-gated.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's document templates")

	// ErrTemplateNotFound means no document_templates row with the given
	// id is visible in the caller's firm context.
	ErrTemplateNotFound = errors.New("document template not found")

	// ErrInvalidTemplate means Create/UpdateDocumentTemplate was given a
	// structurally invalid template (empty name/type/template body, or an
	// unrecognized type).
	ErrInvalidTemplate = errors.New("invalid document template")
)

// TemplateType is document_templates.type's fixed, DB-CHECK-enforced
// vocabulary.
type TemplateType string

const (
	TemplateTypeInvoice      TemplateType = "invoice"
	TemplateTypeDeliveryNote TemplateType = "delivery_note"
	TemplateTypeReport       TemplateType = "report"
	// TemplateTypeContract (contracts management/document workflows/legal
	// foundation batch): document_templates.type's CHECK constraint was
	// extended to include 'contract' (migrations/0031_contracts_management.up.sql)
	// so a firm can author its own contract templates the same way it
	// already authors invoice templates.
	TemplateTypeContract TemplateType = "contract"
)

func validTemplateType(t TemplateType) bool {
	switch t {
	case TemplateTypeInvoice, TemplateTypeDeliveryNote, TemplateTypeReport, TemplateTypeContract:
		return true
	}
	return false
}

// DocumentTemplate is one document_templates row.
type DocumentTemplate struct {
	ID        uuid.UUID
	FirmID    uuid.UUID
	Name      string
	Type      TemplateType
	Template  string
	IsDefault bool
	CreatedAt time.Time
}

// DocumentTemplateInput is Create/UpdateDocumentTemplate's shared field
// shape.
type DocumentTemplateInput struct {
	Name      string
	Type      TemplateType
	Template  string
	IsDefault bool
}

func validateTemplateInput(input DocumentTemplateInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("%w: name must not be empty", ErrInvalidTemplate)
	}
	if !validTemplateType(input.Type) {
		return fmt.Errorf("%w: unknown type %q", ErrInvalidTemplate, input.Type)
	}
	if strings.TrimSpace(input.Template) == "" {
		return fmt.Errorf("%w: template must not be empty", ErrInvalidTemplate)
	}
	return nil
}

const documentTemplateColumns = "id, firm_id, name, type, template, is_default, created_at"

func scanDocumentTemplate(row pgx.Row) (DocumentTemplate, error) {
	var t DocumentTemplate
	if err := row.Scan(&t.ID, &t.FirmID, &t.Name, &t.Type, &t.Template, &t.IsDefault, &t.CreatedAt); err != nil {
		return DocumentTemplate{}, err
	}
	return t, nil
}

// CreateDocumentTemplate adds a new document template to firmID -
// owner-gated, same tier as internal/localization.CreateAddress. When
// IsDefault is set, any other template of the same Type loses its own
// default flag first, in the same transaction - at most one default
// template per (firm, type) at a time.
func CreateDocumentTemplate(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input DocumentTemplateInput) (DocumentTemplate, error) {
	if err := validateTemplateInput(input); err != nil {
		return DocumentTemplate{}, err
	}

	var tpl DocumentTemplate
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		if input.IsDefault {
			if _, err := tx.Exec(ctx, `UPDATE document_templates SET is_default = false WHERE firm_id = $1 AND type = $2`, firmID, input.Type); err != nil {
				return fmt.Errorf("clear existing default template: %w", err)
			}
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO document_templates (firm_id, name, type, template, is_default)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING `+documentTemplateColumns, firmID, strings.TrimSpace(input.Name), input.Type, input.Template, input.IsDefault)
		t, err := scanDocumentTemplate(row)
		if err != nil {
			return fmt.Errorf("insert document template: %w", err)
		}
		tpl = t
		return nil
	})
	if err != nil {
		return DocumentTemplate{}, err
	}
	return tpl, nil
}

// ListDocumentTemplates returns every document template for firmID -
// member-gated read. Ensures the firm has at least one default 'invoice'
// template first (see EnsureDefaultInvoiceTemplate) - this package's own
// minimal invoice template is seeded lazily, on first use, rather than
// requiring every firm-creation path to wire this package in explicitly.
func ListDocumentTemplates(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]DocumentTemplate, error) {
	if err := EnsureDefaultInvoiceTemplate(ctx, pool, firmID, userID); err != nil {
		return nil, err
	}

	var templates []DocumentTemplate
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+documentTemplateColumns+` FROM document_templates WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list document templates: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanDocumentTemplate(rows)
			if err != nil {
				return fmt.Errorf("scan document template: %w", err)
			}
			templates = append(templates, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return templates, nil
}

// GetDocumentTemplate returns one document_templates row - member-gated.
func GetDocumentTemplate(ctx context.Context, pool *pgxpool.Pool, firmID, userID, templateID uuid.UUID) (DocumentTemplate, error) {
	var tpl DocumentTemplate
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+documentTemplateColumns+` FROM document_templates WHERE id = $1 AND firm_id = $2`, templateID, firmID)
		t, err := scanDocumentTemplate(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTemplateNotFound
			}
			return fmt.Errorf("get document template: %w", err)
		}
		tpl = t
		return nil
	})
	if err != nil {
		return DocumentTemplate{}, err
	}
	return tpl, nil
}

// UpdateDocumentTemplate replaces templateID's mutable fields -
// owner-gated, same tier as CreateDocumentTemplate. Full-replace (not a
// partial patch), matching internal/localization's own minimal CRUD
// convention.
func UpdateDocumentTemplate(ctx context.Context, pool *pgxpool.Pool, firmID, userID, templateID uuid.UUID, input DocumentTemplateInput) (DocumentTemplate, error) {
	if err := validateTemplateInput(input); err != nil {
		return DocumentTemplate{}, err
	}

	var tpl DocumentTemplate
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		if input.IsDefault {
			if _, err := tx.Exec(ctx, `UPDATE document_templates SET is_default = false WHERE firm_id = $1 AND type = $2 AND id <> $3`, firmID, input.Type, templateID); err != nil {
				return fmt.Errorf("clear existing default template: %w", err)
			}
		}

		row := tx.QueryRow(ctx, `
			UPDATE document_templates SET name = $1, type = $2, template = $3, is_default = $4
			WHERE id = $5 AND firm_id = $6
			RETURNING `+documentTemplateColumns, strings.TrimSpace(input.Name), input.Type, input.Template, input.IsDefault, templateID, firmID)
		t, err := scanDocumentTemplate(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTemplateNotFound
			}
			return fmt.Errorf("update document template: %w", err)
		}
		tpl = t
		return nil
	})
	if err != nil {
		return DocumentTemplate{}, err
	}
	return tpl, nil
}

// defaultInvoiceTemplate is the minimal default invoice template seeded
// for every firm on first use (see EnsureDefaultInvoiceTemplate) - plain,
// unstyled, but exercises every top-level section RenderInvoice's own
// data map provides (invoice fields, a Lines range, Customer, Firm).
const defaultInvoiceTemplate = `<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>Invoice {{.Invoice.Number}}</title></head>
<body>
  <h1>Invoice {{.Invoice.Number}}</h1>
  <p>{{.Firm.Name}}<br>{{.Firm.Address}}</p>
  <p>Bill to: {{.Customer.Name}}<br>{{.Customer.Address}}</p>
  <p>Issued: {{.Invoice.IssuedDate}} &middot; Due: {{.Invoice.DueDate}}</p>
  <table border="1" cellpadding="4" cellspacing="0">
    <thead>
      <tr><th>Description</th><th>Quantity</th><th>Unit Price</th><th>Line Total</th></tr>
    </thead>
    <tbody>
      {{range .Lines}}<tr><td>{{.Description}}</td><td>{{.Quantity}}</td><td>{{.UnitPrice}}</td><td>{{.LineTotal}}</td></tr>
      {{end}}
    </tbody>
  </table>
  <p>Subtotal: {{.Invoice.Subtotal}}<br>Tax: {{.Invoice.TaxAmount}}<br>Total: {{.Invoice.Total}} {{.Invoice.Currency}}</p>
</body>
</html>
`

// EnsureDefaultInvoiceTemplate inserts defaultInvoiceTemplate for firmID
// if that firm has no 'invoice'-type document template yet - the
// "seed a minimal default invoice template when the package initializes"
// requirement, done lazily per-firm on first use (ListDocumentTemplates,
// RenderInvoice) rather than at Go package init time, since a Go init()
// has no database connection or firmID to seed for - this is the
// equivalent per-firm bootstrap every other seeded-default in this
// codebase (e.g. internal/accounting.SeedDefaultChartOfAccountsTx) uses,
// just triggered on first read instead of at firm-creation time, so an
// existing firm from before this package existed still gets one.
func EnsureDefaultInvoiceTemplate(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM document_templates WHERE firm_id = $1 AND type = $2`, firmID, TemplateTypeInvoice).Scan(&count); err != nil {
			return fmt.Errorf("count invoice templates: %w", err)
		}
		if count > 0 {
			return nil
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO document_templates (firm_id, name, type, template, is_default)
			VALUES ($1, 'Default Invoice', $2, $3, true)
		`, firmID, TemplateTypeInvoice, defaultInvoiceTemplate); err != nil {
			return fmt.Errorf("seed default invoice template: %w", err)
		}
		return nil
	})
}

// defaultContractTemplate is the minimal default contract template
// seeded for every firm on first use (see EnsureDefaultContractTemplate) -
// an NDA-style layout, per this batch's own brief, exercising every
// top-level section RenderContract's own data map provides (contract
// fields, Counterparty, Firm).
const defaultContractTemplate = `<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>{{.Contract.Title}}</title></head>
<body>
  <h1>{{.Contract.Title}}</h1>
  <p>Reference: {{.Contract.ReferenceNumber}}<br>Status: {{.Contract.Status}}</p>
  <p>Between:<br>{{.Firm.Name}}<br>{{.Firm.Address}}</p>
  <p>And:<br>{{.Counterparty.Name}}</p>
  <p>Effective: {{.Contract.StartDate}} &middot; Expires: {{.Contract.EndDate}}</p>
  <h2>Confidentiality</h2>
  <p>The parties agree to keep confidential all non-public information disclosed in connection with this agreement, for the term stated above.</p>
  <h2>Terms</h2>
  <p>{{.Contract.Notes}}</p>
  <p>Value: {{.Contract.Value}} {{.Contract.Currency}}</p>
</body>
</html>
`

// EnsureDefaultContractTemplate inserts defaultContractTemplate for
// firmID if that firm has no 'contract'-type document template yet -
// same lazy per-firm bootstrap EnsureDefaultInvoiceTemplate's own doc
// comment explains.
func EnsureDefaultContractTemplate(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM document_templates WHERE firm_id = $1 AND type = $2`, firmID, TemplateTypeContract).Scan(&count); err != nil {
			return fmt.Errorf("count contract templates: %w", err)
		}
		if count > 0 {
			return nil
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO document_templates (firm_id, name, type, template, is_default)
			VALUES ($1, 'Default NDA', $2, $3, true)
		`, firmID, TemplateTypeContract, defaultContractTemplate); err != nil {
			return fmt.Errorf("seed default contract template: %w", err)
		}
		return nil
	})
}
