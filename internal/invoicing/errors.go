// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package invoicing

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - invoice/payment writes are
	// owner-gated (see this package's doc comment).
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's invoices")

	// ErrInvoiceNotFound means no invoices row with the given id is
	// visible in the caller's firm context.
	ErrInvoiceNotFound = errors.New("invoice not found")

	// ErrInvoiceLineNotFound means no invoice_lines row with the given id
	// is visible on the given invoice.
	ErrInvoiceLineNotFound = errors.New("invoice line not found")

	// ErrInvalidInvoice means CreateInvoice/UpdateInvoiceStatus/
	// CreateInvoiceTx was given a structurally invalid invoice (an
	// unrecognized status, an illegal status transition, no lines).
	ErrInvalidInvoice = errors.New("invalid invoice")

	// ErrInvalidInvoiceLine means AddInvoiceLine/UpdateInvoiceLine was
	// given a structurally invalid line (empty description, non-positive
	// quantity/unit price, malformed tax rate).
	ErrInvalidInvoiceLine = errors.New("invalid invoice line")

	// ErrInvalidPayment means RecordPayment was given a structurally
	// invalid payment (non-positive amount, no paid_at).
	ErrInvalidPayment = errors.New("invalid payment")

	// ErrInvalidFilter means ListOptions.Filters referenced a field/op
	// internal/queryfilter.BuildClause rejected against
	// invoiceFilterFields - see ListInvoices' own doc comment.
	ErrInvalidFilter = errors.New("invalid filter")
)
