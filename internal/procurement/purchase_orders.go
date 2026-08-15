// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package procurement is internal/salesorders' purchase-facing
// counterpart: the structured order record sitting alongside
// internal/workflow's purchase_order definition. Purchase orders are
// created two ways: directly, owner-gated, via CreatePurchaseOrder; or
// automatically, by the workflow-to-purchase-order bridge
// (internal/workflow's TransitionSpec.Effects carrying a
// PurchaseOrderTemplate) via CreatePurchaseOrderTx, when purchase_order's
// "send" transition fires with a resolvable supplier/product/quantity/
// unit_price.
//
// Deliberately a SEPARATE package/table from internal/salesorders, not a
// shared orders table with a `type` discriminator - see
// migrations/0026_sales_purchase_orders.up.sql's own doc comment for the
// full reasoning (different counterparty FK, different fixed status
// vocabulary, no need for either FK to be nullable).
//
// Scope boundary (this batch): no shipping carrier integration, no
// returns/refunds, no multi-warehouse fulfillment routing.
package procurement

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
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/webhook"
)

const purchaseOrderAuditEntityType = "purchase_order"

const (
	createPurchaseOrderAuditAction = "create"
	updatePurchaseOrderAuditAction = "update"
)

const defaultPurchaseOrderCurrency = "TRY"

const purchaseOrderSourceType = "workflow_instance"

// quantityPattern/unitPricePattern/taxRatePattern mirror
// internal/salesorders' own copy of this pattern - see its doc comment.
var (
	quantityPattern  = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)
	unitPricePattern = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)
	taxRatePattern   = regexp.MustCompile(`^[0-9](\.[0-9]{1,4})?$`)
)

func validDecimal(pattern *regexp.Regexp, s string) bool {
	return pattern.MatchString(strings.TrimSpace(s))
}

// PurchaseOrderStatus is purchase_orders.status's fixed, DB-CHECK-enforced
// vocabulary - deliberately matching internal/workflow.PurchaseOrderSpec's
// own draft/sent/received/cancelled states one-to-one, since this record
// is driven by that exact workflow.
type PurchaseOrderStatus string

const (
	PurchaseOrderStatusDraft     PurchaseOrderStatus = "draft"
	PurchaseOrderStatusSent      PurchaseOrderStatus = "sent"
	PurchaseOrderStatusReceived  PurchaseOrderStatus = "received"
	PurchaseOrderStatusCancelled PurchaseOrderStatus = "cancelled"
)

func validPurchaseOrderStatus(s PurchaseOrderStatus) bool {
	switch s {
	case PurchaseOrderStatusDraft, PurchaseOrderStatusSent, PurchaseOrderStatusReceived, PurchaseOrderStatusCancelled:
		return true
	}
	return false
}

var allowedStatusTransitions = map[PurchaseOrderStatus][]PurchaseOrderStatus{
	PurchaseOrderStatusDraft: {PurchaseOrderStatusSent, PurchaseOrderStatusCancelled},
	PurchaseOrderStatusSent:  {PurchaseOrderStatusReceived, PurchaseOrderStatusCancelled},
}

func statusTransitionAllowed(from, to PurchaseOrderStatus) bool {
	for _, allowed := range allowedStatusTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// PurchaseOrder is one purchase_orders row, with its lines.
type PurchaseOrder struct {
	ID                     uuid.UUID
	FirmID                 uuid.UUID
	OrderNumber            string
	SupplierID             *uuid.UUID
	Status                 PurchaseOrderStatus
	Notes                  *string
	Currency               string
	Subtotal               string
	TaxAmount              string
	Total                  string
	SourceWorkflowInstance *uuid.UUID
	CreatedAt              time.Time
	Lines                  []PurchaseOrderLine
}

// PurchaseOrderLine is one purchase_order_lines row.
type PurchaseOrderLine struct {
	ID          uuid.UUID
	OrderID     uuid.UUID
	ProductID   *uuid.UUID
	Description string
	Quantity    string
	UnitPrice   string
	TaxRate     string
	LineTotal   string
}

// PurchaseOrderLineInput is one requested line, on CreatePurchaseOrderInput.
type PurchaseOrderLineInput struct {
	ProductID   *uuid.UUID
	Description string
	Quantity    string
	UnitPrice   string
	TaxRate     string
}

func validateLineInput(l PurchaseOrderLineInput) error {
	if strings.TrimSpace(l.Description) == "" {
		return fmt.Errorf("%w: description must not be empty", ErrInvalidPurchaseOrderLine)
	}
	if !validDecimal(quantityPattern, l.Quantity) {
		return fmt.Errorf("%w: quantity %q must be a positive decimal with at most 4 fraction digits", ErrInvalidPurchaseOrderLine, l.Quantity)
	}
	if !validDecimal(unitPricePattern, l.UnitPrice) {
		return fmt.Errorf("%w: unitPrice %q must be a positive decimal with at most 4 fraction digits", ErrInvalidPurchaseOrderLine, l.UnitPrice)
	}
	taxRate := strings.TrimSpace(l.TaxRate)
	if taxRate != "" && !validDecimal(taxRatePattern, taxRate) {
		return fmt.Errorf("%w: taxRate %q must be a decimal fraction with at most 4 fraction digits", ErrInvalidPurchaseOrderLine, l.TaxRate)
	}
	return nil
}

// nextOrderNumberTx atomically allocates the next sequential purchase
// order number for firmID, formatted "PO-%04d" - the same collision-free
// single-UPSERT pattern internal/salesorders.nextOrderNumberTx uses.
//
// ciaudit:ignore-firmid-check: internal helper, only called by
// insertPurchaseOrderTx - itself only reachable from CreatePurchaseOrder
// (owner-gated) or CreatePurchaseOrderTx (the workflow bridge, already
// authorized by ExecuteTransition).
func nextOrderNumberTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID) (string, error) {
	var claimed int
	if err := tx.QueryRow(ctx, `
		INSERT INTO purchase_order_sequences (firm_id, next_number) VALUES ($1, 2)
		ON CONFLICT (firm_id) DO UPDATE SET next_number = purchase_order_sequences.next_number + 1
		RETURNING next_number - 1
	`, firmID).Scan(&claimed); err != nil {
		return "", fmt.Errorf("allocate purchase order number: %w", err)
	}
	return fmt.Sprintf("PO-%04d", claimed), nil
}

// ciaudit:ignore-firmid-check: shared creation primitive; every caller has
// already run its own authorization check before calling this.
func insertPurchaseOrderTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, supplierID *uuid.UUID, status PurchaseOrderStatus, notes, currency string, lines []PurchaseOrderLineInput, sourceWorkflowInstance *uuid.UUID) (PurchaseOrder, error) {
	if len(lines) == 0 {
		return PurchaseOrder{}, fmt.Errorf("%w: a purchase order must have at least one line", ErrInvalidPurchaseOrder)
	}
	for _, l := range lines {
		if err := validateLineInput(l); err != nil {
			return PurchaseOrder{}, err
		}
	}
	if currency == "" {
		currency = defaultPurchaseOrderCurrency
	}

	orderNumber, err := nextOrderNumberTx(ctx, tx, firmID)
	if err != nil {
		return PurchaseOrder{}, err
	}

	var po PurchaseOrder
	po.FirmID = firmID
	po.OrderNumber = orderNumber
	po.SupplierID = supplierID
	po.Status = status
	po.Currency = currency
	po.SourceWorkflowInstance = sourceWorkflowInstance
	if notes != "" {
		po.Notes = &notes
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO purchase_orders (firm_id, order_number, supplier_id, status, notes, currency, source_workflow_instance)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`, firmID, orderNumber, supplierID, string(status), po.Notes, currency, sourceWorkflowInstance,
	).Scan(&po.ID, &po.CreatedAt); err != nil {
		return PurchaseOrder{}, fmt.Errorf("insert purchase order: %w", err)
	}

	for _, l := range lines {
		taxRate := strings.TrimSpace(l.TaxRate)
		if taxRate == "" {
			taxRate = "0"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO purchase_order_lines (order_id, product_id, description, quantity, unit_price, tax_rate, line_total)
			VALUES ($1, $2, $3, $4::numeric, $5::numeric, $6::numeric, ($4::numeric * $5::numeric))
		`, po.ID, l.ProductID, strings.TrimSpace(l.Description), l.Quantity, l.UnitPrice, taxRate); err != nil {
			return PurchaseOrder{}, fmt.Errorf("insert purchase order line: %w", err)
		}
	}

	totals, err := recomputeTotalsTx(ctx, tx, po.ID)
	if err != nil {
		return PurchaseOrder{}, err
	}
	po.Subtotal, po.TaxAmount, po.Total = totals.Subtotal, totals.TaxAmount, totals.Total

	po.Lines, err = fetchLinesTx(ctx, tx, po.ID)
	if err != nil {
		return PurchaseOrder{}, err
	}

	return po, nil
}

// CreatePurchaseOrder adds a new purchase order (with its lines) to
// firmID directly, always starting 'draft' - owner-gated, same tier
// internal/salesorders.CreateSalesOrder uses.
func CreatePurchaseOrder(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, supplierID *uuid.UUID, notes, currency string, lines []PurchaseOrderLineInput) (PurchaseOrder, error) {
	var po PurchaseOrder
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

		po, err = insertPurchaseOrderTx(ctx, tx, firmID, supplierID, PurchaseOrderStatusDraft, notes, currency, lines, nil)
		if err != nil {
			return err
		}
		return auditlog.Write(ctx, tx, firmID, userID, po.ID, purchaseOrderAuditEntityType, createPurchaseOrderAuditAction, map[string]any{
			"orderNumber": po.OrderNumber, "total": po.Total,
		})
	})
	if err != nil {
		return PurchaseOrder{}, err
	}

	webhook.Dispatch(pool, firmID, webhook.EventPurchaseOrderCreated, map[string]any{
		"purchaseOrderId":     po.ID.String(),
		"purchaseOrderNumber": po.OrderNumber,
		"total":               po.Total,
		"currency":            po.Currency,
	})

	return po, nil
}

// CreatePurchaseOrderTxInput is CreatePurchaseOrderTx's argument shape -
// mirrors internal/salesorders.CreateSalesOrderTxInput.
type CreatePurchaseOrderTxInput struct {
	SupplierID  *uuid.UUID
	Description string
	Quantity    string
	UnitPrice   string
	ProductID   *uuid.UUID
}

// CreatePurchaseOrderTx creates a new purchase order (with exactly one
// line), always starting 'sent' - the workflow's own "send" transition is
// what triggers this bridge (see internal/workflow's PurchaseOrderTemplate),
// so the order this creates reflects that this order has, by definition,
// already been sent - with SourceWorkflowInstance set to instanceID. Same
// "*Tx function, no authorization of its own" pattern
// internal/salesorders.CreateSalesOrderTx already establishes.
//
// ciaudit:ignore-firmid-check: shared creation primitive; the caller
// (internal/workflow's purchase-order-template hook) has already run its
// own authorization check before calling this.
func CreatePurchaseOrderTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, instanceID uuid.UUID, input CreatePurchaseOrderTxInput) (uuid.UUID, error) {
	line := PurchaseOrderLineInput{
		Description: input.Description, Quantity: input.Quantity, UnitPrice: input.UnitPrice, ProductID: input.ProductID,
	}
	po, err := insertPurchaseOrderTx(ctx, tx, firmID, input.SupplierID, PurchaseOrderStatusSent, "", "", []PurchaseOrderLineInput{line}, &instanceID)
	if err != nil {
		return uuid.UUID{}, err
	}
	return po.ID, nil
}

type recomputedTotals struct {
	Subtotal, TaxAmount, Total string
}

func recomputeTotalsTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (recomputedTotals, error) {
	var t recomputedTotals
	if err := tx.QueryRow(ctx, `
		WITH totals AS (
			SELECT
				COALESCE(SUM(line_total), 0) AS subtotal,
				COALESCE(SUM(line_total * tax_rate), 0) AS tax_amount
			FROM purchase_order_lines WHERE order_id = $1
		)
		UPDATE purchase_orders SET
			subtotal = totals.subtotal,
			tax_amount = totals.tax_amount,
			total = totals.subtotal + totals.tax_amount
		FROM totals
		WHERE purchase_orders.id = $1
		RETURNING purchase_orders.subtotal::text, purchase_orders.tax_amount::text, purchase_orders.total::text
	`, orderID).Scan(&t.Subtotal, &t.TaxAmount, &t.Total); err != nil {
		return recomputedTotals{}, fmt.Errorf("recompute purchase order totals: %w", err)
	}
	return t, nil
}

func fetchLinesTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]PurchaseOrderLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, order_id, product_id, description, quantity::text, unit_price::text, tax_rate::text, line_total::text
		FROM purchase_order_lines WHERE order_id = $1 ORDER BY id
	`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list purchase order lines: %w", err)
	}
	defer rows.Close()
	var lines []PurchaseOrderLine
	for rows.Next() {
		var l PurchaseOrderLine
		if err := rows.Scan(&l.ID, &l.OrderID, &l.ProductID, &l.Description, &l.Quantity, &l.UnitPrice, &l.TaxRate, &l.LineTotal); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

func scanPurchaseOrderRow(row pgx.Row) (PurchaseOrder, error) {
	var po PurchaseOrder
	var status string
	if err := row.Scan(&po.ID, &po.FirmID, &po.OrderNumber, &po.SupplierID, &status,
		&po.Notes, &po.Currency, &po.Subtotal, &po.TaxAmount, &po.Total, &po.SourceWorkflowInstance, &po.CreatedAt); err != nil {
		return PurchaseOrder{}, err
	}
	po.Status = PurchaseOrderStatus(status)
	return po, nil
}

const purchaseOrderColumns = `id, firm_id, order_number, supplier_id, status, notes, currency, subtotal::text, tax_amount::text, total::text, source_workflow_instance, created_at`

// ListOptions controls ListPurchaseOrders' optional status filter.
type ListOptions struct {
	Status PurchaseOrderStatus
}

// ListPurchaseOrders returns firmID's purchase orders (without their
// lines), newest first, optionally filtered by status. Member-gated, same
// tier as internal/salesorders.ListSalesOrders.
func ListPurchaseOrders(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListOptions) ([]PurchaseOrder, error) {
	var orders []PurchaseOrder
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		query := `SELECT ` + purchaseOrderColumns + ` FROM purchase_orders`
		args := []any{}
		if opts.Status != "" {
			args = append(args, string(opts.Status))
			query += ` WHERE status = $1`
		}
		query += ` ORDER BY created_at DESC`

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list purchase orders: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			po, err := scanPurchaseOrderRow(rows)
			if err != nil {
				return err
			}
			orders = append(orders, po)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return orders, nil
}

// GetPurchaseOrder resolves one purchase order (with its lines) by id
// within firmID. Member-gated, same tier as ListPurchaseOrders.
func GetPurchaseOrder(ctx context.Context, pool *pgxpool.Pool, firmID, userID, orderID uuid.UUID) (PurchaseOrder, error) {
	var po PurchaseOrder
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+purchaseOrderColumns+` FROM purchase_orders WHERE id = $1 AND firm_id = $2`, orderID, firmID)
		po, err = scanPurchaseOrderRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPurchaseOrderNotFound
		}
		if err != nil {
			return fmt.Errorf("look up purchase order: %w", err)
		}

		po.Lines, err = fetchLinesTx(ctx, tx, orderID)
		return err
	})
	if err != nil {
		return PurchaseOrder{}, err
	}
	return po, nil
}

// UpdatePurchaseOrderStatus moves orderID from its current status to
// newStatus, via allowedStatusTransitions' own small hand-validated state
// machine - mirroring purchase_order's own workflow states, since a
// purchase order's status here is meant to track that workflow instance's
// state, not diverge from it. Owner-gated, same tier as CreatePurchaseOrder.
func UpdatePurchaseOrderStatus(ctx context.Context, pool *pgxpool.Pool, firmID, userID, orderID uuid.UUID, newStatus PurchaseOrderStatus) (PurchaseOrder, error) {
	if !validPurchaseOrderStatus(newStatus) {
		return PurchaseOrder{}, fmt.Errorf("%w: unrecognized status %q", ErrInvalidPurchaseOrder, newStatus)
	}

	var po PurchaseOrder
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

		var currentStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM purchase_orders WHERE id = $1 AND firm_id = $2`, orderID, firmID).Scan(&currentStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrPurchaseOrderNotFound
			}
			return fmt.Errorf("look up purchase order status: %w", err)
		}
		if !statusTransitionAllowed(PurchaseOrderStatus(currentStatus), newStatus) {
			return fmt.Errorf("%w: cannot move a purchase order from %q to %q", ErrInvalidPurchaseOrder, currentStatus, newStatus)
		}

		row := tx.QueryRow(ctx, `
			UPDATE purchase_orders SET status = $1 WHERE id = $2 AND firm_id = $3
			RETURNING `+purchaseOrderColumns, string(newStatus), orderID, firmID)
		po, err = scanPurchaseOrderRow(row)
		if err != nil {
			return fmt.Errorf("update purchase order status: %w", err)
		}
		po.Lines, err = fetchLinesTx(ctx, tx, orderID)
		if err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, orderID, purchaseOrderAuditEntityType, updatePurchaseOrderAuditAction, map[string]any{
			"fromStatus": currentStatus, "toStatus": string(newStatus),
		})
	})
	if err != nil {
		return PurchaseOrder{}, err
	}
	return po, nil
}
