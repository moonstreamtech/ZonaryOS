// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package invoicing is the structural layer connecting internal/accounting,
// internal/crm, and internal/inventory: a real invoice, with real lines,
// generated against a sale - not just a journal entry and a state label.
// Invoices are created two ways: directly, owner-gated, via CreateInvoice;
// or automatically, by internal/workflow's workflow-to-invoicing bridge
// (TransitionSpec.Invoice, see spec.go's own doc comment) via
// CreateInvoiceTx, when a transition carrying an InvoiceTemplate fires
// with a resolvable product/quantity/unit_price. SourceWorkflowInstance
// distinguishes the latter from the former, the same role
// customers.source_workflow_instance/deliveries.source_id already play.
//
// Payments (payments.go) close the receivables cycle: recording a payment
// that brings an invoice's total paid to >= its total auto-marks it
// 'paid' and posts a real DR Cash / CR Trade Receivables journal entry,
// in the same transaction as the payment itself.
//
// Authorization follows this codebase's usual discipline: IsMember before
// any read, IsOwner before any direct write - the same tier as
// internal/crm's customer mutations. CreateInvoiceTx itself is different:
// it's driven by the workflow-to-invoicing bridge, which has already
// checked its own transition's permission - see its own doc comment.
//
// Scope boundary (this batch): no PDF generation (invoices as structured
// data first), no recurring invoices, no credit notes/refunds, no
// multi-currency invoice settlement, no customer payment portal.
package invoicing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/localization"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const invoiceAuditEntityType = "invoice"

const (
	createInvoiceAuditAction = "create"
	updateInvoiceAuditAction = "update"
)

const defaultInvoiceCurrency = "TRY"

// invoiceSourceType is what CreateInvoiceTx records deliveries-style
// provenance under when auto-creating an invoice from a workflow
// transition - mirrors internal/logistics/internal/crm's own
// "workflow_instance" convention (see internal/workflow's
// journalEntitySourceType).
const invoiceSourceType = "workflow_instance"

// quantityPattern/unitPricePattern mirror internal/accounting's own
// amountPattern (numeric(19,4): up to 15 integer digits, 4 fraction
// digits) - redeclared here rather than imported cross-package, same
// reasoning every other package's own amountPattern gives. Positivity is
// enforced by the real DB CHECK (invoice_lines.quantity/unit_price > 0,
// migrations/0015_invoicing_core.up.sql) - the authoritative guarantee,
// this regex is just garbage-input rejection before a query is even
// attempted.
var (
	quantityPattern  = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)
	unitPricePattern = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)
	// taxRatePattern matches invoice_lines.tax_rate's numeric(5,2) column:
	// up to 3 integer digits, 2 fraction digits.
	taxRatePattern = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,2})?$`)
)

func validDecimal(pattern *regexp.Regexp, s string) bool {
	return pattern.MatchString(strings.TrimSpace(s))
}

// InvoiceStatus is invoices.status's fixed, DB-CHECK-enforced vocabulary.
type InvoiceStatus string

const (
	InvoiceStatusDraft     InvoiceStatus = "draft"
	InvoiceStatusSent      InvoiceStatus = "sent"
	InvoiceStatusPaid      InvoiceStatus = "paid"
	InvoiceStatusOverdue   InvoiceStatus = "overdue"
	InvoiceStatusCancelled InvoiceStatus = "cancelled"
)

func validInvoiceStatus(s InvoiceStatus) bool {
	switch s {
	case InvoiceStatusDraft, InvoiceStatusSent, InvoiceStatusPaid, InvoiceStatusOverdue, InvoiceStatusCancelled:
		return true
	}
	return false
}

// allowedStatusTransitions is UpdateInvoiceStatus's own small state
// machine - deliberately NOT internal/workflow's generic engine (the
// design brief's own "keep these simple, no separate workflow; the
// invoice has its own simple status field"): a fixed, five-state,
// hand-validated set, not something a firm ever defines or extends.
// 'paid' is reachable only through RecordPayment (payments.go), not
// listed as a manually-reachable target here - an invoice becomes paid
// by being paid, not by an owner clicking a status dropdown.
var allowedStatusTransitions = map[InvoiceStatus][]InvoiceStatus{
	InvoiceStatusDraft:   {InvoiceStatusSent, InvoiceStatusCancelled},
	InvoiceStatusSent:    {InvoiceStatusOverdue, InvoiceStatusCancelled},
	InvoiceStatusOverdue: {InvoiceStatusSent, InvoiceStatusCancelled},
}

func statusTransitionAllowed(from, to InvoiceStatus) bool {
	for _, allowed := range allowedStatusTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Invoice is one invoices row, with its lines.
type Invoice struct {
	ID                     uuid.UUID
	FirmID                 uuid.UUID
	InvoiceNumber          string
	CustomerID             *uuid.UUID
	IssuedDate             time.Time
	DueDate                *time.Time
	Status                 InvoiceStatus
	Subtotal               string
	TaxAmount              string
	Total                  string
	Currency               string
	Notes                  *string
	SourceWorkflowInstance *uuid.UUID
	CreatedAt              time.Time
	Lines                  []InvoiceLine
}

// InvoiceLine is one invoice_lines row.
type InvoiceLine struct {
	ID          uuid.UUID
	InvoiceID   uuid.UUID
	Description string
	Quantity    string
	UnitPrice   string
	TaxRate     string
	TaxRateID   *uuid.UUID
	LineTotal   string
	ProductID   *uuid.UUID
}

// InvoiceLineInput is one requested line, on CreateInvoiceInput/AddInvoiceLine.
type InvoiceLineInput struct {
	Description string
	Quantity    string // decimal string
	UnitPrice   string // decimal string
	TaxRate     string // decimal string, optional - "" means 0. Ignored
	// when TaxRateID is set - see resolveLineTaxRate.
	//
	// TaxRateID, when set, overrides TaxRate: the referenced
	// internal/localization tax_rates row's own rate (a fraction, e.g.
	// "0.2000") is resolved and converted to invoice_lines.tax_rate's
	// percentage shape (multiplied by 100) at write time - see
	// resolveLineTaxRate's own doc comment for why this is resolved once,
	// not re-derived later.
	TaxRateID *uuid.UUID
	ProductID *uuid.UUID
}

func validateLineInput(l InvoiceLineInput) error {
	if strings.TrimSpace(l.Description) == "" {
		return fmt.Errorf("%w: description must not be empty", ErrInvalidInvoiceLine)
	}
	if !validDecimal(quantityPattern, l.Quantity) {
		return fmt.Errorf("%w: quantity %q must be a positive decimal with at most 4 fraction digits", ErrInvalidInvoiceLine, l.Quantity)
	}
	if !validDecimal(unitPricePattern, l.UnitPrice) {
		return fmt.Errorf("%w: unitPrice %q must be a positive decimal with at most 4 fraction digits", ErrInvalidInvoiceLine, l.UnitPrice)
	}
	taxRate := strings.TrimSpace(l.TaxRate)
	if taxRate != "" && !validDecimal(taxRatePattern, taxRate) {
		return fmt.Errorf("%w: taxRate %q must be a decimal with at most 2 fraction digits", ErrInvalidInvoiceLine, l.TaxRate)
	}
	return nil
}

// resolveLineTaxRate resolves what actually gets written to
// invoice_lines.tax_rate/tax_rate_id for one InvoiceLineInput: if
// TaxRateID is set, the referenced internal/localization tax_rates row
// is looked up (within THIS already-open, already-authorized
// transaction - requireInvoiceOwner has already run in every caller
// before this), its own rate (a fraction, e.g. "0.2000") converted to
// invoice_lines.tax_rate's percentage shape via an exact Postgres
// `numeric` multiply (never a Go float, same discipline every other
// money-adjacent conversion in this codebase follows), and returned
// alongside the tax_rate_id to store. Resolved once, at write time, not
// re-derived later if the tax_rates row subsequently changes or is
// deleted - see migrations/0020's own comment on invoice_lines.tax_rate_id
// for why. Otherwise (TaxRateID unset) falls back to the plain TaxRate
// string, "0" when empty - unchanged from before this batch.
//
// ciaudit:ignore-firmid-check: internal *Tx primitive called only from
// insertInvoiceTx/AddInvoiceLine/UpdateInvoiceLine, each of which has
// already run requireInvoiceOwner (or the workflow bridge's own
// equivalent check) before opening the transaction this runs inside.
func resolveLineTaxRate(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, l InvoiceLineInput) (taxRate string, taxRateID *uuid.UUID, err error) {
	if l.TaxRateID == nil {
		taxRate = strings.TrimSpace(l.TaxRate)
		if taxRate == "" {
			taxRate = "0"
		}
		return taxRate, nil, nil
	}

	rate, err := localization.GetTaxRateTx(ctx, tx, firmID, *l.TaxRateID)
	if err != nil {
		return "", nil, err
	}
	var percentage string
	if err := tx.QueryRow(ctx, `SELECT ($1::numeric * 100)::text`, rate.Rate).Scan(&percentage); err != nil {
		return "", nil, fmt.Errorf("convert tax rate %s to percentage: %w", rate.ID, err)
	}
	return percentage, l.TaxRateID, nil
}

// nextInvoiceNumberTx atomically allocates the next sequential invoice
// number for firmID, formatted "INV-%04d" (INV-0001, INV-0002, ...).
//
// Collision-free under real concurrency: this is a single UPSERT, not a
// read-then-write pair - "INSERT ... ON CONFLICT (firm_id) DO UPDATE"
// takes a row-level lock on invoice_sequences' one row for firmID (either
// the newly-inserted row, or the pre-existing one the ON CONFLICT arm
// updates) for the rest of the surrounding transaction. A second,
// concurrent call for the same firmID blocks at this statement until the
// first transaction commits or rolls back - it can never read the same
// next_number the first call just claimed, so two concurrent invoice
// creations for the same firm always get two different, strictly
// increasing numbers. This is the same pattern a per-tenant auto-
// increment counter needs when a single global Postgres SEQUENCE object
// (database-wide, not firm-scoped) isn't the right shape - see this
// batch's own report for the concurrency test that exercises this
// directly (TestNextInvoiceNumberTx_ConcurrentCallsGetDistinctNumbers).
//
// ciaudit:ignore-firmid-check: internal helper, only called by
// insertInvoiceTx (this file) - itself only reachable from CreateInvoice
// (owner-gated) or CreateInvoiceTx (the workflow bridge, already
// authorized by ExecuteTransition) - firmID here scopes the sequence row,
// it is not itself an authorization decision.
func nextInvoiceNumberTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID) (string, error) {
	var claimed int
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoice_sequences (firm_id, next_number) VALUES ($1, 2)
		ON CONFLICT (firm_id) DO UPDATE SET next_number = invoice_sequences.next_number + 1
		RETURNING next_number - 1
	`, firmID).Scan(&claimed); err != nil {
		return "", fmt.Errorf("allocate invoice number: %w", err)
	}
	return fmt.Sprintf("INV-%04d", claimed), nil
}

// insertInvoiceTx is the shared write primitive both CreateInvoice (the
// owner-gated HTTP path) and CreateInvoiceTx (the workflow bridge) go
// through - allocates the next sequential number, inserts the invoice row
// (status always 'draft' - a newly-created invoice, however it was
// created, always starts undecided/unsent), inserts every line, then
// computes subtotal/tax_amount/total from the lines it just inserted via
// recomputeInvoiceTotalsTx (real Postgres numeric arithmetic, never a Go
// float - see that function's own doc comment).
//
// ciaudit:ignore-firmid-check: shared invoice-creation primitive; every
// caller (CreateInvoice, owner-gated; CreateInvoiceTx, the workflow
// bridge) has already run its own authorization check before calling this.
func insertInvoiceTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, customerID *uuid.UUID, issuedDate time.Time, dueDate *time.Time, currency, notes string, lines []InvoiceLineInput, sourceWorkflowInstance *uuid.UUID) (Invoice, error) {
	if len(lines) == 0 {
		return Invoice{}, fmt.Errorf("%w: an invoice must have at least one line", ErrInvalidInvoice)
	}
	for _, l := range lines {
		if err := validateLineInput(l); err != nil {
			return Invoice{}, err
		}
	}
	if currency == "" {
		currency = defaultInvoiceCurrency
	}

	invoiceNumber, err := nextInvoiceNumberTx(ctx, tx, firmID)
	if err != nil {
		return Invoice{}, err
	}

	var inv Invoice
	inv.FirmID = firmID
	inv.InvoiceNumber = invoiceNumber
	inv.CustomerID = customerID
	inv.IssuedDate = issuedDate
	inv.DueDate = dueDate
	inv.Status = InvoiceStatusDraft
	inv.Currency = currency
	inv.SourceWorkflowInstance = sourceWorkflowInstance
	if notes != "" {
		inv.Notes = &notes
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, customer_id, issued_date, due_date, status, currency, notes, source_workflow_instance)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at
	`, firmID, invoiceNumber, customerID, issuedDate, dueDate, string(InvoiceStatusDraft), currency, inv.Notes, sourceWorkflowInstance,
	).Scan(&inv.ID, &inv.CreatedAt); err != nil {
		return Invoice{}, fmt.Errorf("insert invoice: %w", err)
	}

	for _, l := range lines {
		taxRate, taxRateID, err := resolveLineTaxRate(ctx, tx, firmID, l)
		if err != nil {
			return Invoice{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, tax_rate, tax_rate_id, line_total, product_id)
			VALUES ($1, $2, $3::numeric, $4::numeric, $5::numeric, $6, ($3::numeric * $4::numeric), $7)
		`, inv.ID, strings.TrimSpace(l.Description), l.Quantity, l.UnitPrice, taxRate, taxRateID, l.ProductID); err != nil {
			return Invoice{}, fmt.Errorf("insert invoice line: %w", err)
		}
	}

	totals, err := recomputeInvoiceTotalsTx(ctx, tx, inv.ID)
	if err != nil {
		return Invoice{}, err
	}
	inv.Subtotal, inv.TaxAmount, inv.Total = totals.Subtotal, totals.TaxAmount, totals.Total

	invoiceLines, err := fetchInvoiceLinesTx(ctx, tx, inv.ID)
	if err != nil {
		return Invoice{}, err
	}
	inv.Lines = invoiceLines

	return inv, nil
}

// CreateInvoice adds a new invoice (with its lines) to firmID directly,
// with no SourceWorkflowInstance - owner-gated: creating an invoice by
// hand is a structural firm-financial decision, the same tier as
// internal/accounting.PostJournalEntry.
func CreateInvoice(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, customerID *uuid.UUID, issuedDate time.Time, dueDate *time.Time, currency, notes string, lines []InvoiceLineInput) (Invoice, error) {
	var inv Invoice
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

		inv, err = insertInvoiceTx(ctx, tx, firmID, customerID, issuedDate, dueDate, currency, notes, lines, nil)
		if err != nil {
			return err
		}
		return auditlog.Write(ctx, tx, firmID, userID, inv.ID, invoiceAuditEntityType, createInvoiceAuditAction, map[string]any{
			"invoiceNumber": inv.InvoiceNumber, "total": inv.Total,
		})
	})
	if err != nil {
		return Invoice{}, err
	}
	return inv, nil
}

// CreateInvoiceTxInput is CreateInvoiceTx's argument shape - the fields
// the workflow-to-invoicing bridge (internal/workflow's
// TransitionSpec.Invoice, resolved by engine.go) has to offer: a single
// line, from the sale's own product/quantity/unit_price payload fields
// (the design brief's "auto-create a draft invoice with one line").
type CreateInvoiceTxInput struct {
	CustomerID  *uuid.UUID
	Description string
	Quantity    string
	UnitPrice   string
	ProductID   *uuid.UUID
	IssuedDate  time.Time
}

// CreateInvoiceTx creates a new draft invoice (with exactly one line) on
// tx (an already-open transaction the caller controls), with
// SourceWorkflowInstance set to instanceID - the workflow-to-invoicing
// bridge's own write primitive, the same "*Tx function, no authorization
// of its own, shared by every entry point" pattern
// internal/crm.CreateCustomerTx/internal/logistics.CreateDeliveryTx
// already establish. The caller (internal/workflow.ExecuteTransition) has
// already checked the transition's own permission by the time it reaches
// here.
//
// ciaudit:ignore-firmid-check: shared invoice-creation primitive; every
// caller (internal/workflow's invoice-template hook) has already run its
// own authorization check before calling this.
func CreateInvoiceTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, instanceID uuid.UUID, input CreateInvoiceTxInput) (uuid.UUID, error) {
	line := InvoiceLineInput{
		Description: input.Description, Quantity: input.Quantity, UnitPrice: input.UnitPrice, ProductID: input.ProductID,
	}
	inv, err := insertInvoiceTx(ctx, tx, firmID, input.CustomerID, input.IssuedDate, nil, "", "", []InvoiceLineInput{line}, &instanceID)
	if err != nil {
		return uuid.UUID{}, err
	}
	return inv.ID, nil
}

// recomputedTotals is recomputeInvoiceTotalsTx's return shape.
type recomputedTotals struct {
	Subtotal, TaxAmount, Total string
}

// recomputeInvoiceTotalsTx recomputes invoices.subtotal/tax_amount/total
// from invoiceID's CURRENT invoice_lines rows and writes them back -
// called after every line insert/update/delete (insertInvoiceTx,
// AddInvoiceLine, UpdateInvoiceLine, DeleteInvoiceLine), so the invoice's
// own totals never drift from what its lines actually say. Computed
// entirely in Postgres's own exact `numeric` arithmetic (SUM/multiply
// over invoice_lines directly), never a Go float - the same "financial
// amounts never pass through floating point" discipline
// internal/accounting.PostJournalEntryTx's own balance check already
// establishes. tax_amount is SUM(line_total * tax_rate / 100) - each
// line's own tax_rate applied to its own pre-tax line_total, not one
// invoice-wide rate, since invoice_lines.tax_rate is per-line.
func recomputeInvoiceTotalsTx(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) (recomputedTotals, error) {
	var t recomputedTotals
	if err := tx.QueryRow(ctx, `
		WITH totals AS (
			SELECT
				COALESCE(SUM(line_total), 0) AS subtotal,
				COALESCE(SUM(line_total * tax_rate / 100), 0) AS tax_amount
			FROM invoice_lines WHERE invoice_id = $1
		)
		UPDATE invoices SET
			subtotal = totals.subtotal,
			tax_amount = totals.tax_amount,
			total = totals.subtotal + totals.tax_amount
		FROM totals
		WHERE invoices.id = $1
		RETURNING invoices.subtotal::text, invoices.tax_amount::text, invoices.total::text
	`, invoiceID).Scan(&t.Subtotal, &t.TaxAmount, &t.Total); err != nil {
		return recomputedTotals{}, fmt.Errorf("recompute invoice totals: %w", err)
	}
	return t, nil
}

func fetchInvoiceLinesTx(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) ([]InvoiceLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, invoice_id, description, quantity::text, unit_price::text, tax_rate::text, tax_rate_id, line_total::text, product_id
		FROM invoice_lines WHERE invoice_id = $1 ORDER BY id
	`, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("list invoice lines: %w", err)
	}
	defer rows.Close()
	var lines []InvoiceLine
	for rows.Next() {
		var l InvoiceLine
		if err := rows.Scan(&l.ID, &l.InvoiceID, &l.Description, &l.Quantity, &l.UnitPrice, &l.TaxRate, &l.TaxRateID, &l.LineTotal, &l.ProductID); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

func scanInvoiceRow(row pgx.Row) (Invoice, error) {
	var inv Invoice
	var status string
	err := row.Scan(&inv.ID, &inv.FirmID, &inv.InvoiceNumber, &inv.CustomerID, &inv.IssuedDate, &inv.DueDate, &status,
		&inv.Subtotal, &inv.TaxAmount, &inv.Total, &inv.Currency, &inv.Notes, &inv.SourceWorkflowInstance, &inv.CreatedAt)
	if err != nil {
		return Invoice{}, err
	}
	inv.Status = InvoiceStatus(status)
	return inv, nil
}

const invoiceColumns = `id, firm_id, invoice_number, customer_id, issued_date, due_date, status, subtotal::text, tax_amount::text, total::text, currency, notes, source_workflow_instance, created_at`

// ListOptions controls ListInvoices' optional status filter - a zero
// value lists every invoice, unfiltered.
type ListOptions struct {
	Status InvoiceStatus
}

// ListInvoices returns firmID's invoices (without their lines - callers
// needing line detail use GetInvoice), newest first, optionally filtered
// by status. Member-gated (not owner-gated): reading the invoice list is
// ordinary firm data visibility, the same tier as internal/crm.ListCustomers.
func ListInvoices(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListOptions) ([]Invoice, error) {
	var invoices []Invoice
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		query := `SELECT ` + invoiceColumns + ` FROM invoices`
		args := []any{}
		if opts.Status != "" {
			query += ` WHERE status = $1`
			args = append(args, string(opts.Status))
		}
		query += ` ORDER BY created_at DESC`

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list invoices: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			inv, err := scanInvoiceRow(rows)
			if err != nil {
				return err
			}
			invoices = append(invoices, inv)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return invoices, nil
}

// ListInvoicesWithLines returns firmID's invoices WITH their lines
// batched in via one extra `WHERE invoice_id = ANY($1)` query (the same
// two-query batching shape internal/accounting.ListJournalEntries already
// uses for journal_lines), not one GetInvoice call per invoice - the
// N+1 that internal/portability.ExportFirm used to have. Member-gated,
// same tier as ListInvoices; unlike ListInvoices this is the "I need the
// full lines-included shape for every invoice at once" call, so it skips
// ListInvoices' own status filter (every caller so far wants the whole
// firm).
func ListInvoicesWithLines(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Invoice, error) {
	var invoices []Invoice
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+invoiceColumns+` FROM invoices ORDER BY created_at DESC`)
		if err != nil {
			return fmt.Errorf("list invoices: %w", err)
		}
		invoiceIndex := make(map[uuid.UUID]int)
		for rows.Next() {
			inv, err := scanInvoiceRow(rows)
			if err != nil {
				rows.Close()
				return err
			}
			invoiceIndex[inv.ID] = len(invoices)
			invoices = append(invoices, inv)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()

		if len(invoices) == 0 {
			return nil
		}

		invoiceIDs := make([]uuid.UUID, 0, len(invoices))
		for _, inv := range invoices {
			invoiceIDs = append(invoiceIDs, inv.ID)
		}

		lineRows, err := tx.Query(ctx, `
			SELECT id, invoice_id, description, quantity::text, unit_price::text, tax_rate::text, tax_rate_id, line_total::text, product_id
			FROM invoice_lines WHERE invoice_id = ANY($1) ORDER BY invoice_id, id
		`, invoiceIDs)
		if err != nil {
			return fmt.Errorf("list invoice lines: %w", err)
		}
		defer lineRows.Close()
		for lineRows.Next() {
			var l InvoiceLine
			if err := lineRows.Scan(&l.ID, &l.InvoiceID, &l.Description, &l.Quantity, &l.UnitPrice, &l.TaxRate, &l.TaxRateID, &l.LineTotal, &l.ProductID); err != nil {
				return err
			}
			idx, ok := invoiceIndex[l.InvoiceID]
			if !ok {
				continue
			}
			invoices[idx].Lines = append(invoices[idx].Lines, l)
		}
		return lineRows.Err()
	})
	if err != nil {
		return nil, err
	}
	return invoices, nil
}

// ListInvoicesBySourceInstance returns every invoice (without lines,
// same "summary shape" ListInvoices itself returns) whose
// source_workflow_instance is instanceID - the read side of the
// workflow-instance <-> invoice cross-link (Part 3a of the multi-tenant
// isolation hardening + performance batch): a sale/purchase workflow
// instance can drive automatic invoice creation
// (internal/workflow.CreateInvoiceTx, via a transition's InvoiceTemplate),
// and this is how the workflow-instance detail page's own "Associated
// invoices" section, and internal/workflow's own new
// GET .../workflow-instances/{id}/invoices endpoint, look that back up.
// Member-gated, same tier as ListInvoices; an instance with no invoices
// yet returns an empty slice, not an error.
func ListInvoicesBySourceInstance(ctx context.Context, pool *pgxpool.Pool, firmID, userID, instanceID uuid.UUID) ([]Invoice, error) {
	var invoices []Invoice
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT `+invoiceColumns+` FROM invoices WHERE firm_id = $1 AND source_workflow_instance = $2 ORDER BY created_at DESC
		`, firmID, instanceID)
		if err != nil {
			return fmt.Errorf("list invoices by source instance: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			inv, err := scanInvoiceRow(rows)
			if err != nil {
				return err
			}
			invoices = append(invoices, inv)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return invoices, nil
}

// GetInvoice resolves one invoice (with its lines) by id within firmID.
// Member-gated, same tier as ListInvoices.
func GetInvoice(ctx context.Context, pool *pgxpool.Pool, firmID, userID, invoiceID uuid.UUID) (Invoice, error) {
	var inv Invoice
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+invoiceColumns+` FROM invoices WHERE id = $1 AND firm_id = $2`, invoiceID, firmID)
		inv, err = scanInvoiceRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvoiceNotFound
		}
		if err != nil {
			return fmt.Errorf("look up invoice: %w", err)
		}

		inv.Lines, err = fetchInvoiceLinesTx(ctx, tx, invoiceID)
		return err
	})
	if err != nil {
		return Invoice{}, err
	}
	return inv, nil
}

// UpdateInvoiceStatus moves invoiceID from its current status to newStatus,
// via allowedStatusTransitions' own small hand-validated state machine
// (see its own doc comment for why this is deliberately not
// internal/workflow's generic engine). Owner-gated, same tier as
// CreateInvoice. Rejects a transition to/from 'paid' - that status is
// exclusively RecordPayment's (payments.go) to set, never a manual PATCH.
//
// ciaudit:ignore-firmid-check: this is now a thin pool-opening wrapper -
// the real permission.IsMember/IsOwner checks live in
// updateInvoiceStatusTx, called first thing (before any query) with the
// transaction WithFirmContext opens.
func UpdateInvoiceStatus(ctx context.Context, pool *pgxpool.Pool, firmID, userID, invoiceID uuid.UUID, newStatus InvoiceStatus) (Invoice, error) {
	var inv Invoice
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		inv, err = updateInvoiceStatusTx(ctx, tx, firmID, userID, invoiceID, newStatus)
		return err
	})
	if err != nil {
		return Invoice{}, err
	}
	return inv, nil
}

// updateInvoiceStatusTx is UpdateInvoiceStatus's own core logic, taking
// an already-open tx instead of opening its own - extracted so
// BulkUpdateInvoiceStatus can update several invoices' status inside ONE
// shared transaction (true atomicity, the same "extract a Tx-scoped
// core, share it across a shared transaction" shape
// internal/workflow.executeTransitionTx/BulkExecuteTransition already
// establish for bulk-transition), rather than calling the pool-opening
// UpdateInvoiceStatus once per invoice, which would each commit
// independently.
func updateInvoiceStatusTx(ctx context.Context, tx pgx.Tx, firmID, userID, invoiceID uuid.UUID, newStatus InvoiceStatus) (Invoice, error) {
	if !validInvoiceStatus(newStatus) {
		return Invoice{}, fmt.Errorf("%w: unrecognized status %q", ErrInvalidInvoice, newStatus)
	}
	if newStatus == InvoiceStatusPaid {
		return Invoice{}, fmt.Errorf("%w: an invoice can only become paid by recording a payment, not by a direct status update", ErrInvalidInvoice)
	}

	isMember, err := permission.IsMember(ctx, tx, firmID, userID)
	if err != nil {
		return Invoice{}, err
	}
	if !isMember {
		return Invoice{}, ErrFirmNotFound
	}
	isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
	if err != nil {
		return Invoice{}, err
	}
	if !isOwner {
		return Invoice{}, ErrNotOwner
	}

	var currentStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM invoices WHERE id = $1 AND firm_id = $2`, invoiceID, firmID).Scan(&currentStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Invoice{}, ErrInvoiceNotFound
		}
		return Invoice{}, fmt.Errorf("look up invoice status: %w", err)
	}
	if !statusTransitionAllowed(InvoiceStatus(currentStatus), newStatus) {
		return Invoice{}, fmt.Errorf("%w: cannot move an invoice from %q to %q", ErrInvalidInvoice, currentStatus, newStatus)
	}

	row := tx.QueryRow(ctx, `
		UPDATE invoices SET status = $1 WHERE id = $2 AND firm_id = $3
		RETURNING `+invoiceColumns, string(newStatus), invoiceID, firmID)
	inv, err := scanInvoiceRow(row)
	if err != nil {
		return Invoice{}, fmt.Errorf("update invoice status: %w", err)
	}
	inv.Lines, err = fetchInvoiceLinesTx(ctx, tx, invoiceID)
	if err != nil {
		return Invoice{}, err
	}

	if err := auditlog.Write(ctx, tx, firmID, userID, invoiceID, invoiceAuditEntityType, updateInvoiceAuditAction, map[string]any{
		"fromStatus": currentStatus, "toStatus": string(newStatus),
	}); err != nil {
		return Invoice{}, err
	}
	return inv, nil
}

// BulkUpdateInvoiceStatus moves every invoice in invoiceIDs to newStatus
// at once (Part 4 of the multi-tenant isolation hardening + performance
// batch) - owner-only, same tier as UpdateInvoiceStatus itself.
// Atomicity: every invoice's status update (and its own audit_log entry)
// runs inside ONE shared transaction, not one per invoice - a status
// transition that isn't allowed for ANY invoice in the batch (e.g. one
// already 'cancelled') rolls back every invoice already updated in this
// same call, so there is no partial-success outcome: either every
// invoice in invoiceIDs ends up on newStatus, or none of them do.
//
// ciaudit:ignore-firmid-check: every invoice's actual
// permission.IsMember/IsOwner check happens inside updateInvoiceStatusTx,
// called first thing per invoice (before any query on that invoice) -
// this function itself never touches invoices data before that check.
func BulkUpdateInvoiceStatus(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, invoiceIDs []uuid.UUID, newStatus InvoiceStatus) ([]Invoice, error) {
	if len(invoiceIDs) == 0 {
		return nil, fmt.Errorf("%w: bulk status update requires at least one invoice id", ErrInvalidInvoice)
	}

	var invoices []Invoice
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		for _, invoiceID := range invoiceIDs {
			inv, err := updateInvoiceStatusTx(ctx, tx, firmID, userID, invoiceID, newStatus)
			if err != nil {
				return fmt.Errorf("invoice %s: %w", invoiceID, err)
			}
			invoices = append(invoices, inv)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return invoices, nil
}
