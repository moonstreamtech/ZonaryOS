// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package invoicing_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/invoicing"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/queryfilter"
)

// These tests exercise invoicing/payments/aging against a real Postgres
// instance - RLS isolation and permission enforcement can't be
// meaningfully faked, same convention as every other package's
// integration tests in this codebase. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run invoicing integration tests")
	}
	return adminDSN, appDSN
}

func setupTest(t *testing.T) (adminPool, appPool *pgxpool.Pool) {
	t.Helper()
	adminDSN, appDSN := testPools(t)
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
			invoices, invoice_lines, payments, invoice_sequences, customers, accounts, journal_entries, journal_lines,
			tax_rates, audit_log, permissions,
			workflow_definitions, workflow_states, workflow_transitions, workflow_instances CASCADE
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
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (firm_id, key, name, is_owner) VALUES ($1, 'owner', 'Owner', true) RETURNING id
		`, firmID).Scan(&roleID); err != nil {
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

func seedMember(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmID uuid.UUID, keycloakSubject string) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test Member").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO roles (firm_id, key, name) VALUES ($1, 'member', 'Member') RETURNING id`, firmID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed member role/membership: %v", err)
	}
	return userID
}

// seedCoreAccounts seeds just enough of the chart of accounts (Cash,
// Trade Receivables) for RecordPayment's own DR Cash / CR Trade
// Receivables journal entry to have somewhere to post - mirrors
// internal/accounting's own test helper convention (seedAccount) rather
// than depending on internal/wizard, which this package doesn't import.
func seedCoreAccounts(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID uuid.UUID) {
	t.Helper()
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		for _, a := range []struct{ code, name, typ string }{
			{"1000", "Cash", "asset"},
			{"1100", "Trade Receivables", "asset"},
		} {
			if _, err := tx.Exec(ctx, `INSERT INTO accounts (firm_id, code, name, type) VALUES ($1, $2, $3, $4)`, firmID, a.code, a.name, a.typ); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed core accounts: %v", err)
	}
}

func TestCreateInvoice_OwnerCanCreateAndMemberCanList(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "inv-owner")
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "inv-member")

	inv, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Widget", Quantity: "2", UnitPrice: "10.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	if inv.InvoiceNumber != "INV-0001" {
		t.Errorf("expected the first invoice to be numbered INV-0001, got %q", inv.InvoiceNumber)
	}
	if inv.Status != invoicing.InvoiceStatusDraft {
		t.Errorf("expected a new invoice to start draft, got %q", inv.Status)
	}
	if inv.Subtotal != "20.0000" || inv.Total != "20.0000" {
		t.Errorf("expected subtotal/total 20.0000 (2 * 10.00), got subtotal=%q total=%q", inv.Subtotal, inv.Total)
	}

	if _, err := invoicing.CreateInvoice(ctx, appPool, firmID, memberID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Should Be Denied", Quantity: "1", UnitPrice: "1.00"},
	}); !errors.Is(err, invoicing.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a non-owner CreateInvoice, got %v", err)
	}

	invoices, err := invoicing.ListInvoices(ctx, appPool, firmID, memberID, invoicing.ListOptions{})
	if err != nil {
		t.Fatalf("ListInvoices (member): %v", err)
	}
	if len(invoices) != 1 || invoices[0].ID != inv.ID {
		t.Fatalf("expected the member to see the one seeded invoice, got %+v", invoices)
	}
}

func TestCreateInvoice_SecondInvoiceGetsNextSequentialNumber(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "inv-seq-owner")

	first, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "First", Quantity: "1", UnitPrice: "5.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice (first): %v", err)
	}
	second, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Second", Quantity: "1", UnitPrice: "5.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice (second): %v", err)
	}
	if first.InvoiceNumber != "INV-0001" || second.InvoiceNumber != "INV-0002" {
		t.Fatalf("expected INV-0001 then INV-0002, got %q then %q", first.InvoiceNumber, second.InvoiceNumber)
	}
}

// TestNextInvoiceNumberTx_ConcurrentCallsGetDistinctNumbers is the
// concurrency proof for nextInvoiceNumberTx's own doc comment: N
// concurrent CreateInvoice calls against the SAME firm must all succeed
// and must all get distinct invoice numbers - no collision, no two
// invoices sharing a number, and no gaps beyond what the count itself
// implies.
func TestNextInvoiceNumberTx_ConcurrentCallsGetDistinctNumbers(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "inv-concurrent-owner")

	const n = 10
	var wg sync.WaitGroup
	numbers := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			inv, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
				{Description: fmt.Sprintf("Concurrent %d", i), Quantity: "1", UnitPrice: "1.00"},
			})
			numbers[i] = inv.InvoiceNumber
			errs[i] = err
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent CreateInvoice %d: %v", i, err)
		}
		if seen[numbers[i]] {
			t.Fatalf("invoice number %q was allocated more than once: %v", numbers[i], numbers)
		}
		seen[numbers[i]] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct invoice numbers, got %d: %v", n, len(seen), numbers)
	}
}

func TestAddUpdateDeleteInvoiceLine_RecomputesTotals(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "inv-lines-owner")

	inv, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Widget", Quantity: "1", UnitPrice: "10.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	if inv.Total != "10.0000" {
		t.Fatalf("expected initial total 10.0000, got %q", inv.Total)
	}

	inv, err = invoicing.AddInvoiceLine(ctx, appPool, firmID, ownerID, inv.ID, invoicing.InvoiceLineInput{
		Description: "Gadget", Quantity: "2", UnitPrice: "5.00", TaxRate: "10",
	})
	if err != nil {
		t.Fatalf("AddInvoiceLine: %v", err)
	}
	// 10.00 (widget) + 10.00 (2 * 5.00 gadget) = 20.0000 subtotal;
	// tax = 10.00 * 10% = 1.0000; total = 21.0000.
	if inv.Subtotal != "20.0000" || inv.TaxAmount != "1.0000" || inv.Total != "21.0000" {
		t.Fatalf("expected subtotal=20.0000 tax=1.0000 total=21.0000 after adding a line, got subtotal=%q tax=%q total=%q", inv.Subtotal, inv.TaxAmount, inv.Total)
	}
	if len(inv.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(inv.Lines))
	}
	// fetchInvoiceLinesTx orders by id (a UUID), not insertion order - find
	// the gadget line by description rather than assuming a slice index.
	var gadgetLineID uuid.UUID
	for _, l := range inv.Lines {
		if l.Description == "Gadget" {
			gadgetLineID = l.ID
		}
	}
	if gadgetLineID == uuid.Nil {
		t.Fatalf("expected to find the Gadget line among %+v", inv.Lines)
	}

	inv, err = invoicing.UpdateInvoiceLine(ctx, appPool, firmID, ownerID, inv.ID, gadgetLineID, invoicing.InvoiceLineInput{
		Description: "Gadget (updated)", Quantity: "3", UnitPrice: "5.00", TaxRate: "10",
	})
	if err != nil {
		t.Fatalf("UpdateInvoiceLine: %v", err)
	}
	// 10.00 (widget) + 15.00 (3 * 5.00 gadget) = 25.0000 subtotal.
	if inv.Subtotal != "25.0000" {
		t.Fatalf("expected subtotal 25.0000 after updating the line's quantity, got %q", inv.Subtotal)
	}

	inv, err = invoicing.DeleteInvoiceLine(ctx, appPool, firmID, ownerID, inv.ID, gadgetLineID)
	if err != nil {
		t.Fatalf("DeleteInvoiceLine: %v", err)
	}
	if len(inv.Lines) != 1 || inv.Subtotal != "10.0000" || inv.Total != "10.0000" {
		t.Fatalf("expected 1 line and subtotal/total back to 10.0000 after deleting the gadget line, got %+v", inv)
	}
}

func TestUpdateInvoiceStatus_TransitionsAndRejectsIllegalMoves(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "inv-status-owner")

	inv, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Widget", Quantity: "1", UnitPrice: "10.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}

	inv, err = invoicing.UpdateInvoiceStatus(ctx, appPool, firmID, ownerID, inv.ID, invoicing.InvoiceStatusSent)
	if err != nil {
		t.Fatalf("UpdateInvoiceStatus (draft -> sent): %v", err)
	}
	if inv.Status != invoicing.InvoiceStatusSent {
		t.Errorf("expected status sent, got %q", inv.Status)
	}

	if _, err := invoicing.UpdateInvoiceStatus(ctx, appPool, firmID, ownerID, inv.ID, invoicing.InvoiceStatusDraft); !errors.Is(err, invoicing.ErrInvalidInvoice) {
		t.Fatalf("expected ErrInvalidInvoice for an illegal sent -> draft transition, got %v", err)
	}

	if _, err := invoicing.UpdateInvoiceStatus(ctx, appPool, firmID, ownerID, inv.ID, invoicing.InvoiceStatusPaid); !errors.Is(err, invoicing.ErrInvalidInvoice) {
		t.Fatalf("expected ErrInvalidInvoice for a direct manual move to paid, got %v", err)
	}
}

func TestRecordPayment_ClosesInvoiceAndPostsJournalEntry(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "inv-payment-owner")
	seedCoreAccounts(ctx, t, appPool, firmID)

	inv, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Widget", Quantity: "1", UnitPrice: "100.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	if inv.Total != "100.0000" {
		t.Fatalf("expected total 100.0000, got %q", inv.Total)
	}

	// A partial payment must NOT close the invoice.
	_, inv, err = invoicing.RecordPayment(ctx, appPool, firmID, ownerID, inv.ID, invoicing.RecordPaymentInput{
		Amount: "40.00", PaidAt: time.Now(), Method: "bank_transfer",
	})
	if err != nil {
		t.Fatalf("RecordPayment (partial): %v", err)
	}
	if inv.Status != invoicing.InvoiceStatusDraft {
		t.Fatalf("expected a partial payment to leave the invoice draft (unchanged), got %q", inv.Status)
	}

	// The closing payment: total payments now cover the invoice in full.
	payment, inv, err := invoicing.RecordPayment(ctx, appPool, firmID, ownerID, inv.ID, invoicing.RecordPaymentInput{
		Amount: "60.00", PaidAt: time.Now(), Method: "bank_transfer", Reference: "REF-1",
	})
	if err != nil {
		t.Fatalf("RecordPayment (closing): %v", err)
	}
	if inv.Status != invoicing.InvoiceStatusPaid {
		t.Fatalf("expected the invoice to auto-close to paid once payments cover its total, got %q", inv.Status)
	}
	if payment.Amount != "60.0000" {
		t.Errorf("expected the closing payment's amount to round-trip as 60.0000, got %q", payment.Amount)
	}

	payments, err := invoicing.ListPayments(ctx, appPool, firmID, ownerID, inv.ID)
	if err != nil {
		t.Fatalf("ListPayments: %v", err)
	}
	if len(payments) != 2 {
		t.Fatalf("expected 2 recorded payments, got %d", len(payments))
	}

	var entryCount int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM journal_entries WHERE firm_id = $1 AND source_type = 'invoice' AND source_id = $2
	`, firmID, inv.ID).Scan(&entryCount); err != nil {
		t.Fatalf("query journal_entries: %v", err)
	}
	if entryCount != 1 {
		t.Fatalf("expected exactly 1 journal entry posted for this invoice's closure, got %d", entryCount)
	}

	var debitAmount, creditAmount string
	var debitCode, creditCode string
	if err := adminPool.QueryRow(ctx, `
		SELECT a.code, jl.amount::text FROM journal_lines jl
		JOIN accounts a ON a.id = jl.account_id
		JOIN journal_entries je ON je.id = jl.entry_id
		WHERE je.firm_id = $1 AND je.source_type = 'invoice' AND je.source_id = $2 AND jl.side = 'debit'
	`, firmID, inv.ID).Scan(&debitCode, &debitAmount); err != nil {
		t.Fatalf("query debit line: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		SELECT a.code, jl.amount::text FROM journal_lines jl
		JOIN accounts a ON a.id = jl.account_id
		JOIN journal_entries je ON je.id = jl.entry_id
		WHERE je.firm_id = $1 AND je.source_type = 'invoice' AND je.source_id = $2 AND jl.side = 'credit'
	`, firmID, inv.ID).Scan(&creditCode, &creditAmount); err != nil {
		t.Fatalf("query credit line: %v", err)
	}
	if debitCode != "1000" || debitAmount != "100.0000" {
		t.Errorf("expected DR Cash (1000) 100.0000, got %s %s", debitCode, debitAmount)
	}
	if creditCode != "1100" || creditAmount != "100.0000" {
		t.Errorf("expected CR Trade Receivables (1100) 100.0000, got %s %s", creditCode, creditAmount)
	}
}

func TestRecordPayment_RejectsAgainstCancelledInvoice(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "inv-cancel-owner")
	seedCoreAccounts(ctx, t, appPool, firmID)

	inv, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Widget", Quantity: "1", UnitPrice: "10.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	if _, err := invoicing.UpdateInvoiceStatus(ctx, appPool, firmID, ownerID, inv.ID, invoicing.InvoiceStatusCancelled); err != nil {
		t.Fatalf("UpdateInvoiceStatus (draft -> cancelled): %v", err)
	}

	if _, _, err := invoicing.RecordPayment(ctx, appPool, firmID, ownerID, inv.ID, invoicing.RecordPaymentInput{
		Amount: "10.00", PaidAt: time.Now(),
	}); !errors.Is(err, invoicing.ErrInvalidInvoice) {
		t.Fatalf("expected ErrInvalidInvoice recording a payment against a cancelled invoice, got %v", err)
	}
}

func TestReceivablesAging_BucketsByAge(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "inv-aging-owner")

	// due_date 10 days ago -> "0-30" bucket.
	inv1, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now().AddDate(0, 0, -15), ptrTime(time.Now().AddDate(0, 0, -10)), "", "", []invoicing.InvoiceLineInput{
		{Description: "A", Quantity: "1", UnitPrice: "100.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice (bucket 0-30): %v", err)
	}
	if _, err := invoicing.UpdateInvoiceStatus(ctx, appPool, firmID, ownerID, inv1.ID, invoicing.InvoiceStatusSent); err != nil {
		t.Fatalf("UpdateInvoiceStatus inv1: %v", err)
	}

	// due_date 45 days ago -> "31-60" bucket.
	inv2, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now().AddDate(0, 0, -50), ptrTime(time.Now().AddDate(0, 0, -45)), "", "", []invoicing.InvoiceLineInput{
		{Description: "B", Quantity: "1", UnitPrice: "200.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice (bucket 31-60): %v", err)
	}
	if _, err := invoicing.UpdateInvoiceStatus(ctx, appPool, firmID, ownerID, inv2.ID, invoicing.InvoiceStatusSent); err != nil {
		t.Fatalf("UpdateInvoiceStatus inv2: %v", err)
	}

	// due_date 120 days ago -> "90+" bucket.
	inv3, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now().AddDate(0, 0, -125), ptrTime(time.Now().AddDate(0, 0, -120)), "", "", []invoicing.InvoiceLineInput{
		{Description: "C", Quantity: "1", UnitPrice: "300.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice (bucket 90+): %v", err)
	}
	if _, err := invoicing.UpdateInvoiceStatus(ctx, appPool, firmID, ownerID, inv3.ID, invoicing.InvoiceStatusSent); err != nil {
		t.Fatalf("UpdateInvoiceStatus inv3: %v", err)
	}

	// A draft invoice must NOT appear in any bucket - not yet a real
	// receivable.
	if _, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now().AddDate(0, 0, -200), ptrTime(time.Now().AddDate(0, 0, -200)), "", "", []invoicing.InvoiceLineInput{
		{Description: "Still Draft", Quantity: "1", UnitPrice: "999.00"},
	}); err != nil {
		t.Fatalf("CreateInvoice (draft, should be excluded): %v", err)
	}

	buckets, err := invoicing.ReceivablesAging(ctx, appPool, firmID, ownerID)
	if err != nil {
		t.Fatalf("ReceivablesAging: %v", err)
	}
	if len(buckets) != 4 {
		t.Fatalf("expected all 4 fixed buckets to always appear, got %d: %+v", len(buckets), buckets)
	}

	byLabel := map[string]invoicing.AgingBucket{}
	for _, b := range buckets {
		byLabel[b.Label] = b
	}
	if b := byLabel["0-30"]; b.Count != 1 || b.Outstanding != "100.0000" {
		t.Errorf("expected bucket 0-30 to have 1 invoice / 100.0000 outstanding, got %+v", b)
	}
	if b := byLabel["31-60"]; b.Count != 1 || b.Outstanding != "200.0000" {
		t.Errorf("expected bucket 31-60 to have 1 invoice / 200.0000 outstanding, got %+v", b)
	}
	if b := byLabel["61-90"]; b.Count != 0 {
		t.Errorf("expected bucket 61-90 to be empty, got %+v", b)
	}
	if b := byLabel["90+"]; b.Count != 1 || b.Outstanding != "300.0000" {
		t.Errorf("expected bucket 90+ to have 1 invoice / 300.0000 outstanding, got %+v", b)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// TestListInvoices_NumericFilter is Part 2's own required test case: a
// numeric filter on invoices.total (via internal/queryfilter's shared
// `{field, op, value}` vocabulary) keeps only invoices matching it.
func TestListInvoices_NumericFilter(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm Numeric Filter", "inv-numfilter-owner")

	if _, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Small item", Quantity: "1", UnitPrice: "20.00"},
	}); err != nil {
		t.Fatalf("CreateInvoice (small): %v", err)
	}
	large, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Large item", Quantity: "1", UnitPrice: "500.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice (large): %v", err)
	}

	invoices, err := invoicing.ListInvoices(ctx, appPool, firmID, ownerID, invoicing.ListOptions{
		Filters: []queryfilter.Filter{
			{Field: "total", Op: queryfilter.OpGt, Value: "100"},
		},
	})
	if err != nil {
		t.Fatalf("ListInvoices (numeric filter): %v", err)
	}
	if len(invoices) != 1 || invoices[0].ID != large.ID {
		t.Fatalf("expected only the large invoice (%s, total > 100) to match, got %+v", large.ID, invoices)
	}
}

// TestGetInvoice_OutstandingAfterPartialPayment is Part 3's own required
// test case: the outstanding virtual field on GetInvoice's response is
// correct after a partial payment (total - total paid so far, not
// re-fetched from a stale stored value).
func TestGetInvoice_OutstandingAfterPartialPayment(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm Outstanding", "inv-outstanding-owner")
	seedCoreAccounts(ctx, t, appPool, firmID)

	inv, err := invoicing.CreateInvoice(ctx, appPool, firmID, ownerID, nil, time.Now(), nil, "", "", []invoicing.InvoiceLineInput{
		{Description: "Widget", Quantity: "1", UnitPrice: "100.00"},
	})
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}

	if _, _, err := invoicing.RecordPayment(ctx, appPool, firmID, ownerID, inv.ID, invoicing.RecordPaymentInput{
		Amount: "40.00", PaidAt: time.Now(), Method: "bank_transfer",
	}); err != nil {
		t.Fatalf("RecordPayment (partial): %v", err)
	}

	got, err := invoicing.GetInvoice(ctx, appPool, firmID, ownerID, inv.ID)
	if err != nil {
		t.Fatalf("GetInvoice: %v", err)
	}
	if got.TotalPaid == nil || *got.TotalPaid != "40.0000" {
		t.Fatalf("expected total paid 40.0000 after the partial payment, got %v", got.TotalPaid)
	}
	if got.Outstanding == nil || *got.Outstanding != "60.0000" {
		t.Fatalf("expected outstanding 60.0000 (100 - 40) after the partial payment, got %v", got.Outstanding)
	}
}
