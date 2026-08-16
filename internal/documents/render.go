// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package documents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/contracts"
	"github.com/moonstreamtech/ZonaryOS/internal/crm"
	"github.com/moonstreamtech/ZonaryOS/internal/firm"
	"github.com/moonstreamtech/ZonaryOS/internal/invoicing"
)

// ErrTemplateTypeMismatch means the requested template isn't of the
// entity type being rendered (e.g. a 'delivery_note' template used to
// render an invoice).
var ErrTemplateTypeMismatch = errors.New("document template type does not match the entity being rendered")

// RenderInvoice renders invoiceID against templateID (or, if templateID
// is uuid.Nil, against firmID's default 'invoice' template) using Go's
// stdlib html/template - member-gated, the same read tier as
// internal/invoicing.GetInvoice itself. The template receives the full
// entity data (invoice + lines + customer + firm info), always as a
// nested map[string]any with template.Option("missingkey=zero") set: a
// template referencing a field that doesn't exist on the entity (a typo,
// or a field this package's data map simply doesn't provide) evaluates
// to the zero value of `any` (nil) and renders as an empty string,
// rather than failing the whole render - "structured first, formatted
// later" extends to templates too: a template author iterating on a
// template shouldn't get a 500 for one bad field reference.
//
// ciaudit:ignore-firmid-check: this function makes no direct database
// call of its own - every read (invoicing.GetInvoice, GetDocumentTemplate/
// EnsureDefaultInvoiceTemplate, firm.Get, crm.GetCustomer) is delegated to
// that package's own member-gated function, each of which independently
// checks permission.IsMember before returning anything.
func RenderInvoice(ctx context.Context, pool *pgxpool.Pool, firmID, userID, invoiceID, templateID uuid.UUID) (string, error) {
	inv, err := invoicing.GetInvoice(ctx, pool, firmID, userID, invoiceID)
	if err != nil {
		return "", err
	}

	var tpl DocumentTemplate
	if templateID == uuid.Nil {
		if err := EnsureDefaultInvoiceTemplate(ctx, pool, firmID, userID); err != nil {
			return "", err
		}
		tpl, err = getDefaultTemplate(ctx, pool, firmID, userID, TemplateTypeInvoice)
	} else {
		tpl, err = GetDocumentTemplate(ctx, pool, firmID, userID, templateID)
	}
	if err != nil {
		return "", err
	}
	if tpl.Type != TemplateTypeInvoice {
		return "", ErrTemplateTypeMismatch
	}

	firmMeta, err := firm.Get(ctx, pool, firmID, userID)
	if err != nil {
		return "", err
	}

	var customerData map[string]any
	if inv.CustomerID != nil {
		customer, err := crm.GetCustomer(ctx, pool, firmID, userID, *inv.CustomerID)
		if err != nil && !errors.Is(err, crm.ErrCustomerNotFound) {
			return "", err
		}
		if err == nil {
			customerData = map[string]any{
				"Name":    customer.Name,
				"Email":   derefString(customer.Email),
				"Phone":   derefString(customer.Phone),
				"Address": derefString(customer.Address),
				"TaxID":   derefString(customer.TaxID),
			}
		}
	}

	lines := make([]map[string]any, 0, len(inv.Lines))
	for _, l := range inv.Lines {
		lines = append(lines, map[string]any{
			"Description": l.Description,
			"Quantity":    l.Quantity,
			"UnitPrice":   l.UnitPrice,
			"TaxRate":     l.TaxRate,
			"LineTotal":   l.LineTotal,
		})
	}

	data := map[string]any{
		"Invoice": map[string]any{
			"Number":     inv.InvoiceNumber,
			"Status":     string(inv.Status),
			"IssuedDate": inv.IssuedDate.Format("2006-01-02"),
			"DueDate":    formatOptionalDate(inv.DueDate),
			"Subtotal":   inv.Subtotal,
			"TaxAmount":  inv.TaxAmount,
			"Total":      inv.Total,
			"Currency":   inv.Currency,
			"Notes":      derefString(inv.Notes),
		},
		"Lines":    lines,
		"Customer": customerData,
		"Firm": map[string]any{
			"Name":    firmMeta.Name,
			"Address": derefString(firmMeta.Address),
			"TaxID":   derefString(firmMeta.TaxID),
		},
	}

	return renderTemplate(tpl.Template, data)
}

// RenderContract renders contractID against templateID (or, if templateID
// is uuid.Nil, against firmID's default 'contract' template) - same
// shape as RenderInvoice above (member-gated, delegates every read to
// its owning package's own member-gated function, missingkey=zero so a
// bad template field reference renders empty rather than 500ing).
//
// ciaudit:ignore-firmid-check: this function makes no direct database
// call of its own - every read (contracts.GetContract,
// GetDocumentTemplate/EnsureDefaultContractTemplate, firm.Get) is
// delegated to that package's own member-gated function, each of which
// independently checks permission.IsMember before returning anything.
func RenderContract(ctx context.Context, pool *pgxpool.Pool, firmID, userID, contractID, templateID uuid.UUID) (string, error) {
	c, err := contracts.GetContract(ctx, pool, firmID, userID, contractID)
	if err != nil {
		return "", err
	}

	var tpl DocumentTemplate
	if templateID == uuid.Nil {
		if err := EnsureDefaultContractTemplate(ctx, pool, firmID, userID); err != nil {
			return "", err
		}
		tpl, err = getDefaultTemplate(ctx, pool, firmID, userID, TemplateTypeContract)
	} else {
		tpl, err = GetDocumentTemplate(ctx, pool, firmID, userID, templateID)
	}
	if err != nil {
		return "", err
	}
	if tpl.Type != TemplateTypeContract {
		return "", ErrTemplateTypeMismatch
	}

	firmMeta, err := firm.Get(ctx, pool, firmID, userID)
	if err != nil {
		return "", err
	}

	data := map[string]any{
		"Contract": map[string]any{
			"ReferenceNumber": c.ReferenceNumber,
			"Title":           c.Title,
			"Type":            c.Type,
			"Status":          c.Status,
			"Value":           derefString(c.Value),
			"Currency":        c.Currency,
			"StartDate":       formatOptionalDate(c.StartDate),
			"EndDate":         formatOptionalDate(c.EndDate),
			"Notes":           derefString(c.Notes),
		},
		"Counterparty": map[string]any{
			"Name": c.CounterpartyName,
		},
		"Firm": map[string]any{
			"Name":    firmMeta.Name,
			"Address": derefString(firmMeta.Address),
			"TaxID":   derefString(firmMeta.TaxID),
		},
	}

	return renderTemplate(tpl.Template, data)
}

// renderTemplate parses and executes body against data - the one place
// html/template.Option("missingkey=zero") is set, so every render in
// this package shares the same "missing field renders empty, not an
// error" contract described on RenderInvoice.
func renderTemplate(body string, data map[string]any) (string, error) {
	t, err := template.New("document").Option("missingkey=zero").Parse(body)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}
	return buf.String(), nil
}

// getDefaultTemplate finds firmID's default template of typ (or, absent
// an explicit default, its first template of that type).
//
// ciaudit:ignore-firmid-check: delegates entirely to ListDocumentTemplates,
// which itself checks permission.IsMember before returning anything.
func getDefaultTemplate(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, typ TemplateType) (DocumentTemplate, error) {
	templates, err := ListDocumentTemplates(ctx, pool, firmID, userID)
	if err != nil {
		return DocumentTemplate{}, err
	}
	var fallback *DocumentTemplate
	for i := range templates {
		if templates[i].Type != typ {
			continue
		}
		if templates[i].IsDefault {
			return templates[i], nil
		}
		if fallback == nil {
			fallback = &templates[i]
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	return DocumentTemplate{}, ErrTemplateNotFound
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func formatOptionalDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}
