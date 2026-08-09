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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const (
	addInvoiceLineAuditAction    = "add_line"
	updateInvoiceLineAuditAction = "update_line"
	deleteInvoiceLineAuditAction = "delete_line"
)

// requireInvoiceOwner is the shared "is this caller allowed to mutate
// firmID's invoices at all" check every line-mutation function below
// opens with - same tier as CreateInvoice/UpdateInvoiceStatus.
func requireInvoiceOwner(ctx context.Context, tx pgx.Tx, firmID, userID uuid.UUID) error {
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
	return nil
}

// requireInvoiceInFirm confirms invoiceID is a real invoice within
// firmID - every line mutation below needs this before touching
// invoice_lines, since invoice_lines carries no firm_id of its own (its
// RLS policy already enforces this at the row level, but the app-layer
// check gives ErrInvoiceNotFound instead of a bare "0 rows affected").
//
// ciaudit:ignore-firmid-check: internal helper, only called by this
// package's own owner-gated functions AFTER requireInvoiceOwner has
// already run - firmID here scopes an existence check (defense in depth
// alongside RLS), it is not itself an authorization decision.
func requireInvoiceInFirm(ctx context.Context, tx pgx.Tx, firmID, invoiceID uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM invoices WHERE id = $1 AND firm_id = $2)`, invoiceID, firmID).Scan(&exists); err != nil {
		return fmt.Errorf("look up invoice: %w", err)
	}
	if !exists {
		return ErrInvoiceNotFound
	}
	return nil
}

// AddInvoiceLine adds a new line to an existing invoice and recomputes
// the invoice's own subtotal/tax_amount/total from every line it now
// has (recomputeInvoiceTotalsTx) - owner-gated, same tier as CreateInvoice.
//
// ciaudit:ignore-firmid-check: authorization happens via requireInvoiceOwner
// (this file), which calls permission.IsMember/IsOwner directly - this
// function's own firmID parameter scopes the subsequent query/mutation,
// it does not re-decide authorization itself.
func AddInvoiceLine(ctx context.Context, pool *pgxpool.Pool, firmID, userID, invoiceID uuid.UUID, input InvoiceLineInput) (Invoice, error) {
	if err := validateLineInput(input); err != nil {
		return Invoice{}, err
	}

	var inv Invoice
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireInvoiceOwner(ctx, tx, firmID, userID); err != nil {
			return err
		}
		if err := requireInvoiceInFirm(ctx, tx, firmID, invoiceID); err != nil {
			return err
		}

		taxRate, taxRateID, err := resolveLineTaxRate(ctx, tx, firmID, input)
		if err != nil {
			return err
		}
		var lineID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, tax_rate, tax_rate_id, line_total, product_id)
			VALUES ($1, $2, $3::numeric, $4::numeric, $5::numeric, $6, ($3::numeric * $4::numeric), $7)
			RETURNING id
		`, invoiceID, strings.TrimSpace(input.Description), input.Quantity, input.UnitPrice, taxRate, taxRateID, input.ProductID).Scan(&lineID); err != nil {
			return fmt.Errorf("insert invoice line: %w", err)
		}

		if _, err := recomputeInvoiceTotalsTx(ctx, tx, invoiceID); err != nil {
			return err
		}

		inv, err = fetchInvoiceForResponseTx(ctx, tx, firmID, invoiceID)
		if err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, invoiceID, invoiceAuditEntityType, addInvoiceLineAuditAction, map[string]any{"lineId": lineID})
	})
	if err != nil {
		return Invoice{}, err
	}
	return inv, nil
}

// UpdateInvoiceLine replaces one existing line's fields (a full
// replacement, not a partial update - an invoice line is small enough
// that partial-update's nil-means-unchanged complexity isn't worth it,
// unlike Customer/Delivery's own richer UpdateXInput shapes) and
// recomputes the invoice's totals. Owner-gated.
//
// ciaudit:ignore-firmid-check: authorization happens via requireInvoiceOwner
// (this file), which calls permission.IsMember/IsOwner directly - see
// AddInvoiceLine's identical suppression note above.
func UpdateInvoiceLine(ctx context.Context, pool *pgxpool.Pool, firmID, userID, invoiceID, lineID uuid.UUID, input InvoiceLineInput) (Invoice, error) {
	if err := validateLineInput(input); err != nil {
		return Invoice{}, err
	}

	var inv Invoice
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireInvoiceOwner(ctx, tx, firmID, userID); err != nil {
			return err
		}
		if err := requireInvoiceInFirm(ctx, tx, firmID, invoiceID); err != nil {
			return err
		}

		taxRate, taxRateID, err := resolveLineTaxRate(ctx, tx, firmID, input)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE invoice_lines SET
				description = $1, quantity = $2::numeric, unit_price = $3::numeric,
				tax_rate = $4::numeric, tax_rate_id = $5, line_total = ($2::numeric * $3::numeric), product_id = $6
			WHERE id = $7 AND invoice_id = $8
		`, strings.TrimSpace(input.Description), input.Quantity, input.UnitPrice, taxRate, taxRateID, input.ProductID, lineID, invoiceID)
		if err != nil {
			return fmt.Errorf("update invoice line: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrInvoiceLineNotFound
		}

		if _, err := recomputeInvoiceTotalsTx(ctx, tx, invoiceID); err != nil {
			return err
		}

		inv, err = fetchInvoiceForResponseTx(ctx, tx, firmID, invoiceID)
		if err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, invoiceID, invoiceAuditEntityType, updateInvoiceLineAuditAction, map[string]any{"lineId": lineID})
	})
	if err != nil {
		return Invoice{}, err
	}
	return inv, nil
}

// DeleteInvoiceLine removes one line and recomputes the invoice's totals
// from whatever lines remain. An invoice reduced to zero lines is left as
// zero lines (subtotal/tax_amount/total all recompute to 0) - deleting
// the request's own guardrail against ever shipping a line-less invoice
// lives at creation time (insertInvoiceTx's own "at least one line"
// check), not here; a firm correcting an over-invoiced order needs to be
// able to remove every line without CreateInvoice's own precondition
// getting in the way retroactively. Owner-gated.
//
// ciaudit:ignore-firmid-check: authorization happens via requireInvoiceOwner
// (this file), which calls permission.IsMember/IsOwner directly - see
// AddInvoiceLine's identical suppression note above.
func DeleteInvoiceLine(ctx context.Context, pool *pgxpool.Pool, firmID, userID, invoiceID, lineID uuid.UUID) (Invoice, error) {
	var inv Invoice
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := requireInvoiceOwner(ctx, tx, firmID, userID); err != nil {
			return err
		}
		if err := requireInvoiceInFirm(ctx, tx, firmID, invoiceID); err != nil {
			return err
		}

		tag, err := tx.Exec(ctx, `DELETE FROM invoice_lines WHERE id = $1 AND invoice_id = $2`, lineID, invoiceID)
		if err != nil {
			return fmt.Errorf("delete invoice line: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrInvoiceLineNotFound
		}

		if _, err := recomputeInvoiceTotalsTx(ctx, tx, invoiceID); err != nil {
			return err
		}

		inv, err = fetchInvoiceForResponseTx(ctx, tx, firmID, invoiceID)
		if err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, invoiceID, invoiceAuditEntityType, deleteInvoiceLineAuditAction, map[string]any{"lineId": lineID})
	})
	if err != nil {
		return Invoice{}, err
	}
	return inv, nil
}

// fetchInvoiceForResponseTx re-reads invoiceID (with its lines) after a
// line mutation, so AddInvoiceLine/UpdateInvoiceLine/DeleteInvoiceLine
// can all return the invoice's fresh, post-recompute state - shared
// rather than duplicated across the three.
//
// ciaudit:ignore-firmid-check: internal helper, only called by this
// package's own owner-gated functions AFTER requireInvoiceOwner has
// already run - firmID here scopes a read query (defense in depth
// alongside RLS), it is not itself an authorization decision.
func fetchInvoiceForResponseTx(ctx context.Context, tx pgx.Tx, firmID, invoiceID uuid.UUID) (Invoice, error) {
	row := tx.QueryRow(ctx, `SELECT `+invoiceColumns+` FROM invoices WHERE id = $1 AND firm_id = $2`, invoiceID, firmID)
	inv, err := scanInvoiceRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invoice{}, ErrInvoiceNotFound
	}
	if err != nil {
		return Invoice{}, fmt.Errorf("look up invoice: %w", err)
	}
	inv.Lines, err = fetchInvoiceLinesTx(ctx, tx, invoiceID)
	if err != nil {
		return Invoice{}, err
	}
	return inv, nil
}
