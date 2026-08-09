// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package documents_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/documents"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise document template CRUD, default-template seeding,
// and invoice HTML rendering against a real Postgres instance, same
// convention as every other package's integration tests in this
// codebase. Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL and
// ZONARYOS_TEST_APP_DATABASE_URL are set. See docs/DEVELOPMENT.md.

func setupTest(t *testing.T) (adminPool, appPool *pgxpool.Pool) {
	t.Helper()
	adminDSN := os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN := os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run documents integration tests")
	}
	ctx := context.Background()

	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	adminPool, err := zdb.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)
	if _, err := adminPool.Exec(ctx, `
		TRUNCATE firms, users, roles, role_permissions, user_firm_roles,
			document_templates, invoices, invoice_lines, invoice_sequences,
			customers, audit_log, permissions CASCADE
	`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err = zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(appPool.Close)
	return adminPool, appPool
}

func seedOwner(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmName, keycloakSubject string) (firmID, userID uuid.UUID) {
	t.Helper()
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ($1) RETURNING id`, firmName).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test Owner").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO roles (firm_id, key, name, is_owner) VALUES ($1, 'owner', 'Owner', true) RETURNING id`, firmID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed owner role/membership: %v", err)
	}
	return firmID, userID
}

func TestDocumentTemplates_CreateListGetUpdate(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Doc Firm", "doc-owner")

	tpl, err := documents.CreateDocumentTemplate(ctx, appPool, firmID, userID, documents.DocumentTemplateInput{
		Name: "Custom Invoice", Type: documents.TemplateTypeInvoice, Template: "{{.Invoice.Number}}", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("CreateDocumentTemplate: %v", err)
	}

	templates, err := documents.ListDocumentTemplates(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("ListDocumentTemplates: %v", err)
	}
	// Seeding a firm's first custom default invoice template still leaves
	// EnsureDefaultInvoiceTemplate's own lazy seed out, since a real
	// invoice template already exists by the time List runs.
	if len(templates) != 1 {
		t.Fatalf("expected exactly 1 template, got %d: %+v", len(templates), templates)
	}

	got, err := documents.GetDocumentTemplate(ctx, appPool, firmID, userID, tpl.ID)
	if err != nil {
		t.Fatalf("GetDocumentTemplate: %v", err)
	}
	if got.Name != "Custom Invoice" {
		t.Fatalf("unexpected name: %q", got.Name)
	}

	updated, err := documents.UpdateDocumentTemplate(ctx, appPool, firmID, userID, tpl.ID, documents.DocumentTemplateInput{
		Name: "Renamed Invoice", Type: documents.TemplateTypeInvoice, Template: "{{.Invoice.Number}} v2", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("UpdateDocumentTemplate: %v", err)
	}
	if updated.Name != "Renamed Invoice" {
		t.Fatalf("unexpected updated name: %q", updated.Name)
	}
}

func TestDocumentTemplates_InvalidInputRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Doc Firm", "doc-owner")

	_, err := documents.CreateDocumentTemplate(ctx, appPool, firmID, userID, documents.DocumentTemplateInput{
		Name: "Bad", Type: "not_a_real_type", Template: "x",
	})
	if !errors.Is(err, documents.ErrInvalidTemplate) {
		t.Fatalf("expected ErrInvalidTemplate, got %v", err)
	}
}

// TestEnsureDefaultInvoiceTemplate_Seeded checks the "seed a minimal
// default invoice template when [first used]" requirement: a firm with
// no document templates at all still gets exactly one default 'invoice'
// template the first time ListDocumentTemplates runs.
func TestEnsureDefaultInvoiceTemplate_Seeded(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Doc Firm", "doc-owner")

	templates, err := documents.ListDocumentTemplates(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("ListDocumentTemplates: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("expected exactly 1 seeded default template, got %d: %+v", len(templates), templates)
	}
	if templates[0].Type != documents.TemplateTypeInvoice || !templates[0].IsDefault {
		t.Fatalf("expected a default invoice template, got %+v", templates[0])
	}

	// Calling it again must not seed a second one.
	again, err := documents.ListDocumentTemplates(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("ListDocumentTemplates (second call): %v", err)
	}
	if len(again) != 1 {
		t.Fatalf("expected seeding to be idempotent, got %d templates", len(again))
	}
}

// TestRenderInvoice_UsesDefaultTemplate exercises RenderInvoice end to
// end: a real seeded invoice (with a line and a customer) rendered
// against the firm's lazily-seeded default template must produce HTML
// containing the invoice's own data.
func TestRenderInvoice_UsesDefaultTemplate(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Doc Firm", "doc-owner")

	var customerID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO customers (firm_id, name, currency) VALUES ($1, 'Acme Corp', 'TRY') RETURNING id
	`, firmID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	var invoiceID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, customer_id, issued_date, status, subtotal, tax_amount, total, currency)
		VALUES ($1, 'INV-0001', $2, $3, 'draft', 100, 0, 100, 'TRY') RETURNING id
	`, firmID, customerID, time.Now()).Scan(&invoiceID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, tax_rate, line_total)
		VALUES ($1, 'Consulting', 1, 100, 0, 100)
	`, invoiceID); err != nil {
		t.Fatalf("seed invoice line: %v", err)
	}

	html, err := documents.RenderInvoice(ctx, appPool, firmID, userID, invoiceID, uuid.Nil)
	if err != nil {
		t.Fatalf("RenderInvoice: %v", err)
	}
	for _, want := range []string{"INV-0001", "Acme Corp", "Consulting"} {
		if !strings.Contains(html, want) {
			t.Fatalf("expected rendered HTML to contain %q, got:\n%s", want, html)
		}
	}
}
