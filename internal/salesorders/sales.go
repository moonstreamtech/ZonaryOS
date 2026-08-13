// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package salesorders is the structured order record sitting alongside
// internal/workflow's stock_to_sale definition: a real order, with real
// lines and a fulfillment-shaped status
// (draft/confirmed/picking/shipped/delivered/cancelled), not just a
// two-state workflow instance. Sales orders are created two ways:
// directly, owner-gated, via CreateSalesOrder; or automatically, by the
// workflow-to-sales-order bridge (internal/workflow's
// TransitionSpec.Effects carrying a SalesOrderTemplate) via
// CreateSalesOrderTx, when stock_to_sale's record_sale transition fires
// with a resolvable product/quantity/unit_price - the same
// "SourceWorkflowInstance distinguishes bridge-created from
// manually-created" convention internal/invoicing already established
// for invoices (which this package deliberately mirrors: same
// per-firm-sequential order numbering shape, same *Tx bridge-primitive
// pattern, same owner-gated-direct-write / bridge-authorized-automatic
// split).
//
// Scope boundary (this batch): no shipping carrier integration, no
// returns/refunds, no multi-warehouse fulfillment routing.
package salesorders

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

const salesOrderAuditEntityType = "sales_order"

const (
	createSalesOrderAuditAction = "create"
	updateSalesOrderAuditAction = "update"
)

const defaultSalesOrderCurrency = "TRY"

// salesOrderSourceType mirrors internal/invoicing's own invoiceSourceType
// convention for provenance recorded on a bridge-created row.
const salesOrderSourceType = "workflow_instance"

// quantityPattern/unitPricePattern/taxRatePattern mirror
// internal/invoicing's own regexes (numeric(19,4) for
// quantity/unit_price, numeric(5,4) for tax_rate here since
// sales_order_lines.tax_rate is a fraction, not a percentage - see
// migrations/0026_sales_purchase_orders.up.sql). Real positivity is
// enforced by the DB CHECKs; this is just garbage-input rejection before
// a query is attempted, same discipline as every other package's own
// copy of this pattern.
var (
	quantityPattern  = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)
	unitPricePattern = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)
	taxRatePattern   = regexp.MustCompile(`^[0-9](\.[0-9]{1,4})?$`)
)

func validDecimal(pattern *regexp.Regexp, s string) bool {
	return pattern.MatchString(strings.TrimSpace(s))
}

// SalesOrderStatus is sales_orders.status's fixed, DB-CHECK-enforced
// vocabulary.
type SalesOrderStatus string

const (
	SalesOrderStatusDraft     SalesOrderStatus = "draft"
	SalesOrderStatusConfirmed SalesOrderStatus = "confirmed"
	SalesOrderStatusPicking   SalesOrderStatus = "picking"
	SalesOrderStatusShipped   SalesOrderStatus = "shipped"
	SalesOrderStatusDelivered SalesOrderStatus = "delivered"
	SalesOrderStatusCancelled SalesOrderStatus = "cancelled"
)

func validSalesOrderStatus(s SalesOrderStatus) bool {
	switch s {
	case SalesOrderStatusDraft, SalesOrderStatusConfirmed, SalesOrderStatusPicking,
		SalesOrderStatusShipped, SalesOrderStatusDelivered, SalesOrderStatusCancelled:
		return true
	}
	return false
}

// allowedStatusTransitions is UpdateSalesOrderStatus's own small,
// hand-validated state machine - the fulfillment pipeline the design
// brief describes (quote/draft -> order -> fulfilled), not
// internal/workflow's generic engine (same reasoning
// internal/invoicing.allowedStatusTransitions' own doc comment gives for
// invoices: a fixed, small set a firm never redefines). 'cancelled' is
// reachable from every non-terminal status; 'delivered' and 'cancelled'
// are both terminal.
var allowedStatusTransitions = map[SalesOrderStatus][]SalesOrderStatus{
	SalesOrderStatusDraft:     {SalesOrderStatusConfirmed, SalesOrderStatusCancelled},
	SalesOrderStatusConfirmed: {SalesOrderStatusPicking, SalesOrderStatusCancelled},
	SalesOrderStatusPicking:   {SalesOrderStatusShipped, SalesOrderStatusCancelled},
	SalesOrderStatusShipped:   {SalesOrderStatusDelivered, SalesOrderStatusCancelled},
}

func statusTransitionAllowed(from, to SalesOrderStatus) bool {
	for _, allowed := range allowedStatusTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// SalesOrder is one sales_orders row, with its lines.
type SalesOrder struct {
	ID                     uuid.UUID
	FirmID                 uuid.UUID
	OrderNumber            string
	CustomerID             *uuid.UUID
	Status                 SalesOrderStatus
	ShippingAddress        *string
	Notes                  *string
	Currency               string
	Subtotal               string
	TaxAmount              string
	Total                  string
	SourceWorkflowInstance *uuid.UUID
	CreatedAt              time.Time
	Lines                  []SalesOrderLine
}

// SalesOrderLine is one sales_order_lines row.
type SalesOrderLine struct {
	ID          uuid.UUID
	OrderID     uuid.UUID
	ProductID   *uuid.UUID
	Description string
	Quantity    string
	UnitPrice   string
	TaxRate     string
	LineTotal   string
}

// SalesOrderLineInput is one requested line, on CreateSalesOrderInput.
type SalesOrderLineInput struct {
	ProductID   *uuid.UUID
	Description string
	Quantity    string // decimal string
	UnitPrice   string // decimal string
	TaxRate     string // decimal string, optional - "" means 0
}

func validateLineInput(l SalesOrderLineInput) error {
	if strings.TrimSpace(l.Description) == "" {
		return fmt.Errorf("%w: description must not be empty", ErrInvalidSalesOrderLine)
	}
	if !validDecimal(quantityPattern, l.Quantity) {
		return fmt.Errorf("%w: quantity %q must be a positive decimal with at most 4 fraction digits", ErrInvalidSalesOrderLine, l.Quantity)
	}
	if !validDecimal(unitPricePattern, l.UnitPrice) {
		return fmt.Errorf("%w: unitPrice %q must be a positive decimal with at most 4 fraction digits", ErrInvalidSalesOrderLine, l.UnitPrice)
	}
	taxRate := strings.TrimSpace(l.TaxRate)
	if taxRate != "" && !validDecimal(taxRatePattern, taxRate) {
		return fmt.Errorf("%w: taxRate %q must be a decimal fraction with at most 4 fraction digits", ErrInvalidSalesOrderLine, l.TaxRate)
	}
	return nil
}

// nextOrderNumberTx atomically allocates the next sequential sales order
// number for firmID, formatted "SO-%04d" - the exact same collision-free
// single-UPSERT pattern internal/invoicing.nextInvoiceNumberTx uses (see
// its own doc comment for the concurrency proof); redeclared here rather
// than shared cross-package since each package owns its own sequence
// table (sales_order_sequences vs invoice_sequences).
//
// ciaudit:ignore-firmid-check: internal helper, only called by
// insertSalesOrderTx - itself only reachable from CreateSalesOrder
// (owner-gated) or CreateSalesOrderTx (the workflow bridge, already
// authorized by ExecuteTransition) - firmID here scopes the sequence row,
// it is not itself an authorization decision.
func nextOrderNumberTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID) (string, error) {
	var claimed int
	if err := tx.QueryRow(ctx, `
		INSERT INTO sales_order_sequences (firm_id, next_number) VALUES ($1, 2)
		ON CONFLICT (firm_id) DO UPDATE SET next_number = sales_order_sequences.next_number + 1
		RETURNING next_number - 1
	`, firmID).Scan(&claimed); err != nil {
		return "", fmt.Errorf("allocate sales order number: %w", err)
	}
	return fmt.Sprintf("SO-%04d", claimed), nil
}

// insertSalesOrderTx is the shared write primitive both CreateSalesOrder
// (the owner-gated HTTP path) and CreateSalesOrderTx (the workflow
// bridge) go through - allocates the next sequential number, inserts the
// order row at the given initial status, inserts every line, then
// computes subtotal/tax_amount/total from the lines it just inserted.
//
// ciaudit:ignore-firmid-check: shared creation primitive; every caller
// has already run its own authorization check before calling this.
func insertSalesOrderTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, customerID *uuid.UUID, status SalesOrderStatus, shippingAddress, notes, currency string, lines []SalesOrderLineInput, sourceWorkflowInstance *uuid.UUID) (SalesOrder, error) {
	if len(lines) == 0 {
		return SalesOrder{}, fmt.Errorf("%w: a sales order must have at least one line", ErrInvalidSalesOrder)
	}
	for _, l := range lines {
		if err := validateLineInput(l); err != nil {
			return SalesOrder{}, err
		}
	}
	if currency == "" {
		currency = defaultSalesOrderCurrency
	}

	orderNumber, err := nextOrderNumberTx(ctx, tx, firmID)
	if err != nil {
		return SalesOrder{}, err
	}

	var so SalesOrder
	so.FirmID = firmID
	so.OrderNumber = orderNumber
	so.CustomerID = customerID
	so.Status = status
	so.Currency = currency
	so.SourceWorkflowInstance = sourceWorkflowInstance
	if shippingAddress != "" {
		so.ShippingAddress = &shippingAddress
	}
	if notes != "" {
		so.Notes = &notes
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO sales_orders (firm_id, order_number, customer_id, status, shipping_address, notes, currency, source_workflow_instance)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at
	`, firmID, orderNumber, customerID, string(status), so.ShippingAddress, so.Notes, currency, sourceWorkflowInstance,
	).Scan(&so.ID, &so.CreatedAt); err != nil {
		return SalesOrder{}, fmt.Errorf("insert sales order: %w", err)
	}

	for _, l := range lines {
		taxRate := strings.TrimSpace(l.TaxRate)
		if taxRate == "" {
			taxRate = "0"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO sales_order_lines (order_id, product_id, description, quantity, unit_price, tax_rate, line_total)
			VALUES ($1, $2, $3, $4::numeric, $5::numeric, $6::numeric, ($4::numeric * $5::numeric))
		`, so.ID, l.ProductID, strings.TrimSpace(l.Description), l.Quantity, l.UnitPrice, taxRate); err != nil {
			return SalesOrder{}, fmt.Errorf("insert sales order line: %w", err)
		}
	}

	totals, err := recomputeTotalsTx(ctx, tx, so.ID)
	if err != nil {
		return SalesOrder{}, err
	}
	so.Subtotal, so.TaxAmount, so.Total = totals.Subtotal, totals.TaxAmount, totals.Total

	so.Lines, err = fetchLinesTx(ctx, tx, so.ID)
	if err != nil {
		return SalesOrder{}, err
	}

	return so, nil
}

// CreateSalesOrder adds a new sales order (with its lines) to firmID
// directly, always starting 'draft', with no SourceWorkflowInstance -
// owner-gated: creating a sales order by hand is a structural firm
// decision, the same tier internal/invoicing.CreateInvoice uses.
func CreateSalesOrder(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, customerID *uuid.UUID, shippingAddress, notes, currency string, lines []SalesOrderLineInput) (SalesOrder, error) {
	var so SalesOrder
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

		so, err = insertSalesOrderTx(ctx, tx, firmID, customerID, SalesOrderStatusDraft, shippingAddress, notes, currency, lines, nil)
		if err != nil {
			return err
		}
		return auditlog.Write(ctx, tx, firmID, userID, so.ID, salesOrderAuditEntityType, createSalesOrderAuditAction, map[string]any{
			"orderNumber": so.OrderNumber, "total": so.Total,
		})
	})
	if err != nil {
		return SalesOrder{}, err
	}

	webhook.Dispatch(pool, firmID, webhook.EventSalesOrderCreated, map[string]any{
		"salesOrderId":     so.ID.String(),
		"salesOrderNumber": so.OrderNumber,
		"total":            so.Total,
		"currency":         so.Currency,
	})

	return so, nil
}

// CreateSalesOrderTxInput is CreateSalesOrderTx's argument shape - the
// fields the workflow-to-sales-order bridge (internal/workflow's
// TransitionSpec.Effects carrying a SalesOrderTemplate) has to offer: a
// single line, from the sale's own product/quantity/unit_price payload
// fields - the same "auto-create with one line" shape
// internal/invoicing.CreateInvoiceTxInput already establishes.
type CreateSalesOrderTxInput struct {
	CustomerID      *uuid.UUID
	Description     string
	Quantity        string
	UnitPrice       string
	ProductID       *uuid.UUID
	ShippingAddress string
}

// CreateSalesOrderTx creates a new sales order (with exactly one line),
// always starting 'confirmed' - unlike CreateSalesOrder's manual 'draft'
// start, a sale that already fired stock_to_sale's record_sale is, by
// definition, an already-confirmed sale (the state change and the
// journal/stock/invoice bridges have already committed by the time this
// runs, in the SAME transaction - see internal/workflow's executeTransitionTx)
// - with SourceWorkflowInstance set to instanceID. The workflow-to-sales-order
// bridge's own write primitive, the same "*Tx function, no authorization
// of its own, shared by every entry point" pattern
// internal/invoicing.CreateInvoiceTx already establishes. The caller
// (internal/workflow.ExecuteTransition) has already checked the
// transition's own permission by the time it reaches here.
//
// ciaudit:ignore-firmid-check: shared creation primitive; the caller
// (internal/workflow's sales-order-template hook) has already run its own
// authorization check before calling this.
//
// Deliberately does NOT dispatch a webhook: this runs inside the
// caller's still-open transaction (executeTransitionTx) - same
// "dispatching here could notify an external system about a row the
// outer transition still rolls back" reasoning
// internal/invoicing.CreateInvoiceTx's own doc comment gives.
func CreateSalesOrderTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, instanceID uuid.UUID, input CreateSalesOrderTxInput) (uuid.UUID, error) {
	line := SalesOrderLineInput{
		Description: input.Description, Quantity: input.Quantity, UnitPrice: input.UnitPrice, ProductID: input.ProductID,
	}
	so, err := insertSalesOrderTx(ctx, tx, firmID, input.CustomerID, SalesOrderStatusConfirmed, input.ShippingAddress, "", "", []SalesOrderLineInput{line}, &instanceID)
	if err != nil {
		return uuid.UUID{}, err
	}
	return so.ID, nil
}

type recomputedTotals struct {
	Subtotal, TaxAmount, Total string
}

// recomputeTotalsTx recomputes sales_orders.subtotal/tax_amount/total
// from orderID's CURRENT sales_order_lines rows and writes them back -
// the same "computed entirely in Postgres's own exact numeric arithmetic,
// never a Go float" discipline internal/invoicing.recomputeInvoiceTotalsTx
// already establishes. tax_rate here is a fraction (numeric(5,4), see
// migrations/0026's own column comment), not a percentage the way
// invoice_lines.tax_rate is - so tax_amount is
// SUM(line_total * tax_rate), not SUM(line_total * tax_rate / 100).
func recomputeTotalsTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (recomputedTotals, error) {
	var t recomputedTotals
	if err := tx.QueryRow(ctx, `
		WITH totals AS (
			SELECT
				COALESCE(SUM(line_total), 0) AS subtotal,
				COALESCE(SUM(line_total * tax_rate), 0) AS tax_amount
			FROM sales_order_lines WHERE order_id = $1
		)
		UPDATE sales_orders SET
			subtotal = totals.subtotal,
			tax_amount = totals.tax_amount,
			total = totals.subtotal + totals.tax_amount
		FROM totals
		WHERE sales_orders.id = $1
		RETURNING sales_orders.subtotal::text, sales_orders.tax_amount::text, sales_orders.total::text
	`, orderID).Scan(&t.Subtotal, &t.TaxAmount, &t.Total); err != nil {
		return recomputedTotals{}, fmt.Errorf("recompute sales order totals: %w", err)
	}
	return t, nil
}

func fetchLinesTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]SalesOrderLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, order_id, product_id, description, quantity::text, unit_price::text, tax_rate::text, line_total::text
		FROM sales_order_lines WHERE order_id = $1 ORDER BY id
	`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list sales order lines: %w", err)
	}
	defer rows.Close()
	var lines []SalesOrderLine
	for rows.Next() {
		var l SalesOrderLine
		if err := rows.Scan(&l.ID, &l.OrderID, &l.ProductID, &l.Description, &l.Quantity, &l.UnitPrice, &l.TaxRate, &l.LineTotal); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

func scanSalesOrderRow(row pgx.Row) (SalesOrder, error) {
	var so SalesOrder
	var status string
	if err := row.Scan(&so.ID, &so.FirmID, &so.OrderNumber, &so.CustomerID, &status, &so.ShippingAddress,
		&so.Notes, &so.Currency, &so.Subtotal, &so.TaxAmount, &so.Total, &so.SourceWorkflowInstance, &so.CreatedAt); err != nil {
		return SalesOrder{}, err
	}
	so.Status = SalesOrderStatus(status)
	return so, nil
}

const salesOrderColumns = `id, firm_id, order_number, customer_id, status, shipping_address, notes, currency, subtotal::text, tax_amount::text, total::text, source_workflow_instance, created_at`

// ListOptions controls ListSalesOrders' optional status filter - a zero
// value lists every order, unfiltered.
type ListOptions struct {
	Status SalesOrderStatus
}

// ListSalesOrders returns firmID's sales orders (without their lines -
// callers needing line detail use GetSalesOrder), newest first,
// optionally filtered by status. Member-gated, same tier as
// internal/invoicing.ListInvoices.
func ListSalesOrders(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListOptions) ([]SalesOrder, error) {
	var orders []SalesOrder
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		query := `SELECT ` + salesOrderColumns + ` FROM sales_orders`
		args := []any{}
		if opts.Status != "" {
			args = append(args, string(opts.Status))
			query += ` WHERE status = $1`
		}
		query += ` ORDER BY created_at DESC`

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list sales orders: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			so, err := scanSalesOrderRow(rows)
			if err != nil {
				return err
			}
			orders = append(orders, so)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return orders, nil
}

// GetSalesOrder resolves one sales order (with its lines) by id within
// firmID. Member-gated, same tier as ListSalesOrders.
func GetSalesOrder(ctx context.Context, pool *pgxpool.Pool, firmID, userID, orderID uuid.UUID) (SalesOrder, error) {
	var so SalesOrder
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+salesOrderColumns+` FROM sales_orders WHERE id = $1 AND firm_id = $2`, orderID, firmID)
		so, err = scanSalesOrderRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSalesOrderNotFound
		}
		if err != nil {
			return fmt.Errorf("look up sales order: %w", err)
		}

		so.Lines, err = fetchLinesTx(ctx, tx, orderID)
		return err
	})
	if err != nil {
		return SalesOrder{}, err
	}
	return so, nil
}

// UpdateSalesOrderStatus moves orderID from its current status to
// newStatus, via allowedStatusTransitions' own small hand-validated state
// machine. Owner-gated, same tier as CreateSalesOrder.
func UpdateSalesOrderStatus(ctx context.Context, pool *pgxpool.Pool, firmID, userID, orderID uuid.UUID, newStatus SalesOrderStatus) (SalesOrder, error) {
	if !validSalesOrderStatus(newStatus) {
		return SalesOrder{}, fmt.Errorf("%w: unrecognized status %q", ErrInvalidSalesOrder, newStatus)
	}

	var so SalesOrder
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
		if err := tx.QueryRow(ctx, `SELECT status FROM sales_orders WHERE id = $1 AND firm_id = $2`, orderID, firmID).Scan(&currentStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSalesOrderNotFound
			}
			return fmt.Errorf("look up sales order status: %w", err)
		}
		if !statusTransitionAllowed(SalesOrderStatus(currentStatus), newStatus) {
			return fmt.Errorf("%w: cannot move a sales order from %q to %q", ErrInvalidSalesOrder, currentStatus, newStatus)
		}

		row := tx.QueryRow(ctx, `
			UPDATE sales_orders SET status = $1 WHERE id = $2 AND firm_id = $3
			RETURNING `+salesOrderColumns, string(newStatus), orderID, firmID)
		so, err = scanSalesOrderRow(row)
		if err != nil {
			return fmt.Errorf("update sales order status: %w", err)
		}
		so.Lines, err = fetchLinesTx(ctx, tx, orderID)
		if err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, orderID, salesOrderAuditEntityType, updateSalesOrderAuditAction, map[string]any{
			"fromStatus": currentStatus, "toStatus": string(newStatus),
		})
	})
	if err != nil {
		return SalesOrder{}, err
	}
	return so, nil
}
