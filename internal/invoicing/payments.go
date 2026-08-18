// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package invoicing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/integration"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/webhook"
)

const paymentAuditEntityType = "payment"
const recordPaymentAuditAction = "record"

// paymentAmountPattern mirrors internal/accounting's own amountPattern -
// positivity is the real DB CHECK (payments.amount > 0,
// migrations/0015_invoicing_core.up.sql), this is garbage-input
// rejection before a query is attempted.
var paymentAmountPattern = quantityPattern

// Payment is one payments row.
type Payment struct {
	ID        uuid.UUID
	FirmID    uuid.UUID
	InvoiceID *uuid.UUID
	Amount    string
	Currency  string
	PaidAt    time.Time
	Method    *string
	Reference *string
	CreatedAt time.Time
}

// RecordPaymentInput is RecordPayment's request shape.
type RecordPaymentInput struct {
	Amount    string // decimal string
	Currency  string
	PaidAt    time.Time
	Method    string
	Reference string
}

// RecordPayment is the receivables cycle's closing half: inserts a
// payments row against invoiceID, then - in the SAME transaction - checks
// whether this invoice's total recorded payments now cover its own
// total. If so, it moves the invoice to 'paid' AND posts a real
// DR Cash / CR Trade Receivables journal entry
// (internal/accounting.PostJournalEntryTx), sourced to this invoice (the
// same "sourceType/sourceID" provenance convention every other bridge in
// this codebase already uses, here "invoice"/invoiceID rather than
// "workflow_instance"/instanceID, since an invoice - not a workflow
// instance - is what actually closed). Both the payment and the
// invoice-closing side effects commit or roll back together: a payment
// can never end up recorded without its closing consequences (or vice
// versa). See this batch's own report for the full design reasoning.
//
// Owner-gated: recording a real payment against a firm's receivables is
// a structural financial decision, the same tier as
// internal/accounting.PostJournalEntry itself.
//
// ciaudit:ignore-firmid-check: authorization happens via requireInvoiceOwner
// (lines.go), which calls permission.IsMember/IsOwner directly - this
// function's own firmID parameter scopes the subsequent queries/mutations,
// it does not re-decide authorization itself.
func RecordPayment(ctx context.Context, pool *pgxpool.Pool, firmID, userID, invoiceID uuid.UUID, input RecordPaymentInput) (Payment, Invoice, error) {
	amount := strings.TrimSpace(input.Amount)
	if !paymentAmountPattern.MatchString(amount) {
		return Payment{}, Invoice{}, fmt.Errorf("%w: amount %q must be a positive decimal with at most 4 fraction digits", ErrInvalidPayment, input.Amount)
	}
	if input.PaidAt.IsZero() {
		return Payment{}, Invoice{}, fmt.Errorf("%w: paidAt must be set", ErrInvalidPayment)
	}
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = defaultInvoiceCurrency
	}

	var payment Payment
	var inv Invoice
	var justPaid bool
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireInvoiceOwner(ctx, tx, firmID, userID); err != nil {
			return err
		}

		var invoiceTotal string
		var invoiceStatus string
		if err := tx.QueryRow(ctx, `
			SELECT total::text, status FROM invoices WHERE id = $1 AND firm_id = $2
		`, invoiceID, firmID).Scan(&invoiceTotal, &invoiceStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvoiceNotFound
			}
			return fmt.Errorf("look up invoice: %w", err)
		}
		if invoiceStatus == string(InvoiceStatusCancelled) {
			return fmt.Errorf("%w: cannot record a payment against a cancelled invoice", ErrInvalidInvoice)
		}

		payment.FirmID = firmID
		payment.InvoiceID = &invoiceID
		payment.Amount = amount
		payment.Currency = currency
		payment.PaidAt = input.PaidAt
		if m := strings.TrimSpace(input.Method); m != "" {
			payment.Method = &m
		}
		if r := strings.TrimSpace(input.Reference); r != "" {
			payment.Reference = &r
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO payments (firm_id, invoice_id, amount, currency, paid_at, method, reference)
			VALUES ($1, $2, $3::numeric, $4, $5, $6, $7)
			RETURNING id, amount::text, created_at
		`, firmID, invoiceID, amount, currency, input.PaidAt, payment.Method, payment.Reference,
		).Scan(&payment.ID, &payment.Amount, &payment.CreatedAt); err != nil {
			return fmt.Errorf("insert payment: %w", err)
		}

		if err := auditlog.Write(ctx, tx, firmID, userID, payment.ID, paymentAuditEntityType, recordPaymentAuditAction, map[string]any{
			"invoiceId": invoiceID, "amount": amount,
		}); err != nil {
			return err
		}

		// The invoice-closing check: computed by Postgres's own exact
		// `numeric` arithmetic (SUM over this invoice's own payments,
		// compared against invoices.total directly) - never a Go float,
		// the same discipline recomputeInvoiceTotalsTx's own doc comment
		// establishes. A difference >= 0 means total payments now cover
		// the invoice in full (or overpay it - this batch does not reject
		// an overpayment, see the report's own scope note).
		var fullyPaid bool
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(amount), 0) >= (SELECT total FROM invoices WHERE id = $1)
			FROM payments WHERE invoice_id = $1
		`, invoiceID).Scan(&fullyPaid); err != nil {
			return fmt.Errorf("check invoice paid-in-full: %w", err)
		}

		if fullyPaid && invoiceStatus != string(InvoiceStatusPaid) {
			if _, err := tx.Exec(ctx, `UPDATE invoices SET status = $1, updated_at = now() WHERE id = $2`, string(InvoiceStatusPaid), invoiceID); err != nil {
				return fmt.Errorf("mark invoice paid: %w", err)
			}
			justPaid = true

			lines := []accounting.LineInput{
				{AccountCode: accounting.CashAccountCode, Side: accounting.SideDebit, Amount: invoiceTotal},
				{AccountCode: accounting.TradeReceivablesAccountCode, Side: accounting.SideCredit, Amount: invoiceTotal},
			}
			description := fmt.Sprintf("Payment received for invoice %s", invoiceID)
			if _, err := accounting.PostJournalEntryTx(ctx, tx, firmID, userID, description, invoicePaidSourceType, &invoiceID, lines); err != nil {
				return err
			}
		}

		var err error
		inv, err = fetchInvoiceForResponseTx(ctx, tx, firmID, invoiceID)
		return err
	})
	if err != nil {
		return Payment{}, Invoice{}, err
	}
	if justPaid {
		webhook.Dispatch(pool, firmID, webhook.EventInvoicePaid, map[string]any{
			"invoiceId":     invoiceID.String(),
			"invoiceNumber": inv.InvoiceNumber,
			"total":         inv.Total,
			"currency":      inv.Currency,
			"paymentId":     payment.ID.String(),
		})
		// integration.DispatchOutbound (data pipeline/ETL/system
		// integrations batch's own field-mapped entity-sync push) runs
		// side by side with webhook.Dispatch above - see
		// internal/integration/outbound.go's own package doc comment for
		// why the two are kept independent. internal/invoicing can import
		// internal/integration directly (unlike internal/inventory/
		// internal/crm, both of which internal/integration's own inbound
		// pull imports back, which would cycle) - internal/integration
		// never imports internal/invoicing.
		integration.DispatchOutbound(pool, firmID, "invoices", map[string]any{
			"invoiceId":     invoiceID.String(),
			"invoiceNumber": inv.InvoiceNumber,
			"total":         inv.Total,
			"currency":      inv.Currency,
			"paymentId":     payment.ID.String(),
		})
	}
	return payment, inv, nil
}

// invoicePaidSourceType is what the DR Cash / CR Trade Receivables
// journal entry RecordPayment posts on invoice closure records
// journal_entries.source_type as - "invoice", not "workflow_instance":
// this entry is sourced to the invoice that closed, not to whatever
// workflow instance (if any) originally created that invoice.
const invoicePaidSourceType = "invoice"

// ListPayments returns invoiceID's recorded payments, oldest first.
// Member-gated (not owner-gated): reading payment history is ordinary
// firm data visibility, the same tier as ListInvoices.
func ListPayments(ctx context.Context, pool *pgxpool.Pool, firmID, userID, invoiceID uuid.UUID) ([]Payment, error) {
	var payments []Payment
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		if err := requireInvoiceInFirm(ctx, tx, firmID, invoiceID); err != nil {
			return err
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, invoice_id, amount::text, currency, paid_at, method, reference, created_at
			FROM payments WHERE invoice_id = $1 ORDER BY paid_at
		`, invoiceID)
		if err != nil {
			return fmt.Errorf("list payments: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p Payment
			if err := rows.Scan(&p.ID, &p.FirmID, &p.InvoiceID, &p.Amount, &p.Currency, &p.PaidAt, &p.Method, &p.Reference, &p.CreatedAt); err != nil {
				return err
			}
			payments = append(payments, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return payments, nil
}
