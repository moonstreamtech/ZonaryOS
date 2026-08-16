// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package manufacturing

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
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const productionOrderAuditEntityType = "production_order"

const (
	createProductionOrderAuditAction   = "create"
	startProductionOrderAuditAction    = "start"
	completeProductionOrderAuditAction = "complete"
	cancelProductionOrderAuditAction   = "cancel"
)

// materialIssueReason/finishedGoodsReason are stock_movements.reason's
// own open, app-validated vocabulary (see internal/inventory's own
// stock_movements.reason doc comment) - this batch's two additions,
// exactly as the design brief names them.
const (
	materialIssueReason = "production_issue"
	finishedGoodsReason = "production_complete"
)

// productionOrderSourceType mirrors internal/payroll's own
// payrollClosedSourceType convention: what AdjustStockTx/PostJournalEntryTx
// record as journal_entries.source_type/stock_movements.source_type when
// driven by a production order, rather than "workflow_instance" - the
// source here is the production order itself.
const productionOrderSourceType = "production_order"

// ProductionOrderStatus is production_orders.status's fixed,
// DB-CHECK-enforced vocabulary.
type ProductionOrderStatus string

const (
	ProductionOrderStatusPlanned    ProductionOrderStatus = "planned"
	ProductionOrderStatusInProgress ProductionOrderStatus = "in_progress"
	ProductionOrderStatusCompleted  ProductionOrderStatus = "completed"
	ProductionOrderStatusCancelled  ProductionOrderStatus = "cancelled"
)

// ProductionOrder is one production_orders row, with its material issues.
type ProductionOrder struct {
	ID               uuid.UUID
	FirmID           uuid.UUID
	OrderNumber      string
	ProductID        uuid.UUID
	BOMID            uuid.UUID
	QuantityPlanned  string
	QuantityProduced string
	Status           ProductionOrderStatus
	PlannedStart     *time.Time
	PlannedEnd       *time.Time
	ActualStart      *time.Time
	ActualEnd        *time.Time
	Notes            *string
	CreatedAt        time.Time
	MaterialIssues   []MaterialIssue
}

// MaterialIssue is one production_order_material_issues row.
type MaterialIssue struct {
	ID                uuid.UUID
	ProductionOrderID uuid.UUID
	ProductID         uuid.UUID
	QuantityIssued    string
	IssuedAt          time.Time
}

// nextOrderNumberTx atomically allocates the next sequential production
// order number for firmID, formatted "MO-%04d" - the same collision-free
// single-UPSERT pattern internal/invoicing/internal/salesorders/
// internal/procurement's own nextOrderNumberTx-shaped functions use.
//
// ciaudit:ignore-firmid-check: internal helper, only called by
// CreateProductionOrder, which has already run its own authorization
// check before calling this.
func nextOrderNumberTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID) (string, error) {
	var claimed int
	if err := tx.QueryRow(ctx, `
		INSERT INTO production_order_sequences (firm_id, next_number) VALUES ($1, 2)
		ON CONFLICT (firm_id) DO UPDATE SET next_number = production_order_sequences.next_number + 1
		RETURNING next_number - 1
	`, firmID).Scan(&claimed); err != nil {
		return "", fmt.Errorf("allocate production order number: %w", err)
	}
	return fmt.Sprintf("MO-%04d", claimed), nil
}

// CreateProductionOrderInput is CreateProductionOrder's request shape.
type CreateProductionOrderInput struct {
	ProductID       uuid.UUID
	BOMID           uuid.UUID
	QuantityPlanned string // decimal string
	PlannedStart    *time.Time
	PlannedEnd      *time.Time
	Notes           string
}

// CreateProductionOrder adds a new production order to firmID, starting
// 'planned' - owner-gated, same tier CreateBOM uses. Rejects a BOM that
// doesn't actually belong to the given product (ErrInvalidProductionOrder)
// - a production order's BOM and product must agree, or the material-issue
// calculation at Start time would be issuing the wrong product's recipe.
func CreateProductionOrder(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateProductionOrderInput) (ProductionOrder, error) {
	if !validPositiveQuantity(input.QuantityPlanned) {
		return ProductionOrder{}, fmt.Errorf("%w: quantityPlanned %q must be a positive decimal with at most 4 fraction digits", ErrInvalidProductionOrder, input.QuantityPlanned)
	}

	var po ProductionOrder
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

		var bomProductID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT product_id FROM bom_headers WHERE id = $1 AND firm_id = $2`, input.BOMID, firmID).Scan(&bomProductID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrBOMNotFound
			}
			return fmt.Errorf("look up BOM: %w", err)
		}
		if bomProductID != input.ProductID {
			return fmt.Errorf("%w: BOM %s is for a different product", ErrInvalidProductionOrder, input.BOMID)
		}

		orderNumber, err := nextOrderNumberTx(ctx, tx, firmID)
		if err != nil {
			return err
		}

		po.FirmID = firmID
		po.OrderNumber = orderNumber
		po.ProductID = input.ProductID
		po.BOMID = input.BOMID
		po.QuantityPlanned = input.QuantityPlanned
		po.Status = ProductionOrderStatusPlanned
		po.PlannedStart = input.PlannedStart
		po.PlannedEnd = input.PlannedEnd
		if strings.TrimSpace(input.Notes) != "" {
			notes := strings.TrimSpace(input.Notes)
			po.Notes = &notes
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO production_orders (firm_id, order_number, product_id, bom_id, quantity_planned, status, planned_start, planned_end, notes)
			VALUES ($1, $2, $3, $4, $5::numeric, $6, $7, $8, $9)
			RETURNING id, quantity_produced::text, created_at
		`, firmID, orderNumber, input.ProductID, input.BOMID, input.QuantityPlanned, string(ProductionOrderStatusPlanned), po.PlannedStart, po.PlannedEnd, po.Notes,
		).Scan(&po.ID, &po.QuantityProduced, &po.CreatedAt); err != nil {
			return fmt.Errorf("insert production order: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, po.ID, productionOrderAuditEntityType, createProductionOrderAuditAction, map[string]any{
			"orderNumber": po.OrderNumber, "quantityPlanned": po.QuantityPlanned,
		})
	})
	if err != nil {
		return ProductionOrder{}, err
	}
	return po, nil
}

func fetchMaterialIssuesTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]MaterialIssue, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, production_order_id, product_id, quantity_issued::text, issued_at
		FROM production_order_material_issues WHERE production_order_id = $1 ORDER BY issued_at
	`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list material issues: %w", err)
	}
	defer rows.Close()
	var issues []MaterialIssue
	for rows.Next() {
		var m MaterialIssue
		if err := rows.Scan(&m.ID, &m.ProductionOrderID, &m.ProductID, &m.QuantityIssued, &m.IssuedAt); err != nil {
			return nil, err
		}
		issues = append(issues, m)
	}
	return issues, rows.Err()
}

func scanProductionOrderRow(row pgx.Row) (ProductionOrder, error) {
	var po ProductionOrder
	var status string
	if err := row.Scan(&po.ID, &po.FirmID, &po.OrderNumber, &po.ProductID, &po.BOMID, &po.QuantityPlanned, &po.QuantityProduced,
		&status, &po.PlannedStart, &po.PlannedEnd, &po.ActualStart, &po.ActualEnd, &po.Notes, &po.CreatedAt); err != nil {
		return ProductionOrder{}, err
	}
	po.Status = ProductionOrderStatus(status)
	return po, nil
}

const productionOrderColumns = `id, firm_id, order_number, product_id, bom_id, quantity_planned::text, quantity_produced::text,
	status, planned_start, planned_end, actual_start, actual_end, notes, created_at`

// ListOptions controls ListProductionOrders' optional status filter.
type ListOptions struct {
	Status ProductionOrderStatus
}

// ListProductionOrders returns firmID's production orders (without their
// material issues), newest first, optionally filtered by status.
// Member-gated.
func ListProductionOrders(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListOptions) ([]ProductionOrder, error) {
	var orders []ProductionOrder
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		query := `SELECT ` + productionOrderColumns + ` FROM production_orders`
		args := []any{}
		if opts.Status != "" {
			args = append(args, string(opts.Status))
			query += ` WHERE status = $1`
		}
		query += ` ORDER BY created_at DESC`

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list production orders: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			po, err := scanProductionOrderRow(rows)
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

// GetProductionOrder resolves one production order (with its material
// issue log) by id within firmID. Member-gated.
func GetProductionOrder(ctx context.Context, pool *pgxpool.Pool, firmID, userID, orderID uuid.UUID) (ProductionOrder, error) {
	var po ProductionOrder
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+productionOrderColumns+` FROM production_orders WHERE id = $1 AND firm_id = $2`, orderID, firmID)
		po, err = scanProductionOrderRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProductionOrderNotFound
		}
		if err != nil {
			return fmt.Errorf("look up production order: %w", err)
		}

		po.MaterialIssues, err = fetchMaterialIssuesTx(ctx, tx, orderID)
		return err
	})
	if err != nil {
		return ProductionOrder{}, err
	}
	return po, nil
}

// CancelProductionOrder moves orderID from 'planned' or 'in_progress' to
// 'cancelled' - owner-gated. Deliberately does NOT reverse any materials
// already issued (a production order started, then cancelled, has
// genuinely consumed those components - "cancel" cancels the remaining
// work, not the physical reality of what already left stock): stock is
// left exactly as it was at the moment of cancellation, whether that's
// untouched (cancelling a 'planned' order, which never issued anything)
// or already-reduced by the Start-time material issue (cancelling an
// 'in_progress' order).
func CancelProductionOrder(ctx context.Context, pool *pgxpool.Pool, firmID, userID, orderID uuid.UUID) (ProductionOrder, error) {
	var po ProductionOrder
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
		if err := tx.QueryRow(ctx, `SELECT status FROM production_orders WHERE id = $1 AND firm_id = $2 FOR UPDATE`, orderID, firmID).Scan(&currentStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrProductionOrderNotFound
			}
			return fmt.Errorf("look up production order: %w", err)
		}
		if ProductionOrderStatus(currentStatus) != ProductionOrderStatusPlanned && ProductionOrderStatus(currentStatus) != ProductionOrderStatusInProgress {
			return fmt.Errorf("%w: cannot cancel a production order in status %q", ErrInvalidProductionOrderTransition, currentStatus)
		}

		row := tx.QueryRow(ctx, `
			UPDATE production_orders SET status = $1 WHERE id = $2 AND firm_id = $3
			RETURNING `+productionOrderColumns, string(ProductionOrderStatusCancelled), orderID, firmID)
		po, err = scanProductionOrderRow(row)
		if err != nil {
			return fmt.Errorf("cancel production order: %w", err)
		}
		po.MaterialIssues, err = fetchMaterialIssuesTx(ctx, tx, orderID)
		if err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, orderID, productionOrderAuditEntityType, cancelProductionOrderAuditAction, map[string]any{
			"fromStatus": currentStatus,
		})
	})
	if err != nil {
		return ProductionOrder{}, err
	}
	return po, nil
}

// StartProductionOrder moves orderID from 'planned' to 'in_progress' and,
// in the SAME transaction, auto-issues materials: for every bom_lines row
// on the order's BOM, deducts quantity_per_unit * (quantity_planned /
// bom_headers.unit_yield) from that component's stock
// (internal/inventory.AdjustStockTx, reason "production_issue"), and
// records a production_order_material_issues row for exactly that amount.
// Owner-gated.
//
// Atomicity (mirrors internal/payroll.ClosePeriod's own documented
// shape): one transaction - (1) permission checks, (2) SELECT ... FOR
// UPDATE locks the order row (serializes concurrent Start calls on the
// same order), (3) reject if not 'planned', (4) load the BOM's lines,
// (5) for each line, AdjustStockTx (which itself locks/upserts the
// component's stock_levels row and rejects with ErrInsufficientStock if
// the deduction would take it below zero) then insert the matching
// material_issues row, (6) flip status to 'in_progress' and set
// actual_start. Any failure anywhere - most notably ErrInsufficientStock
// on ANY one component - rolls back the WHOLE transaction: every
// component issue already applied in this same call, and the status
// flip, together. There is no partial-issue outcome: either every
// component in the BOM is deducted and the order moves to 'in_progress',
// or none of it happens and the order stays 'planned', safely retryable
// once stock is replenished.
func StartProductionOrder(ctx context.Context, pool *pgxpool.Pool, firmID, userID, orderID uuid.UUID) (ProductionOrder, error) {
	var po ProductionOrder
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

		var bomID uuid.UUID
		var quantityPlanned, currentStatus string
		if err := tx.QueryRow(ctx, `
			SELECT bom_id, quantity_planned::text, status FROM production_orders
			WHERE id = $1 AND firm_id = $2 FOR UPDATE
		`, orderID, firmID).Scan(&bomID, &quantityPlanned, &currentStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrProductionOrderNotFound
			}
			return fmt.Errorf("look up production order: %w", err)
		}
		if ProductionOrderStatus(currentStatus) != ProductionOrderStatusPlanned {
			return fmt.Errorf("%w: cannot start a production order in status %q", ErrInvalidProductionOrderTransition, currentStatus)
		}

		var unitYield string
		if err := tx.QueryRow(ctx, `SELECT unit_yield::text FROM bom_headers WHERE id = $1 AND firm_id = $2`, bomID, firmID).Scan(&unitYield); err != nil {
			return fmt.Errorf("look up BOM unit yield: %w", err)
		}

		rows, err := tx.Query(ctx, `
			SELECT component_product_id, quantity_per_unit::text FROM bom_lines WHERE bom_id = $1
		`, bomID)
		if err != nil {
			return fmt.Errorf("list BOM lines: %w", err)
		}
		type componentNeed struct {
			productID uuid.UUID
			quantity  string
		}
		var needs []componentNeed
		for rows.Next() {
			var n componentNeed
			if err := rows.Scan(&n.productID, &n.quantity); err != nil {
				rows.Close()
				return err
			}
			needs = append(needs, n)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()

		for _, n := range needs {
			// needed = quantity_per_unit * (quantity_planned / unit_yield) -
			// computed in Postgres's own exact numeric arithmetic, never a
			// Go float, same discipline every other quantity/amount
			// computation in this codebase follows.
			var needed string
			if err := tx.QueryRow(ctx, `SELECT ($1::numeric * ($2::numeric / $3::numeric))::numeric(19,4)::text`, n.quantity, quantityPlanned, unitYield).Scan(&needed); err != nil {
				return fmt.Errorf("compute component quantity needed: %w", err)
			}

			if err := inventory.AdjustStockTx(ctx, tx, firmID, n.productID, nil, "-"+needed, materialIssueReason, productionOrderSourceType, &orderID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO production_order_material_issues (firm_id, production_order_id, product_id, quantity_issued)
				VALUES ($1, $2, $3, $4::numeric)
			`, firmID, orderID, n.productID, needed); err != nil {
				return fmt.Errorf("record material issue: %w", err)
			}
		}

		row := tx.QueryRow(ctx, `
			UPDATE production_orders SET status = $1, actual_start = now() WHERE id = $2 AND firm_id = $3
			RETURNING `+productionOrderColumns, string(ProductionOrderStatusInProgress), orderID, firmID)
		po, err = scanProductionOrderRow(row)
		if err != nil {
			return fmt.Errorf("start production order: %w", err)
		}
		po.MaterialIssues, err = fetchMaterialIssuesTx(ctx, tx, orderID)
		if err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, orderID, productionOrderAuditEntityType, startProductionOrderAuditAction, map[string]any{
			"componentsIssued": len(needs),
		})
	})
	if err != nil {
		return ProductionOrder{}, err
	}
	return po, nil
}

// CompleteProductionOrder moves orderID from 'in_progress' to
// 'completed': adds quantityProduced (the actual quantity made, which may
// differ from quantity_planned) to the finished product's stock
// (internal/inventory.AdjustStockTx, reason "production_complete"), then
// posts a single balanced journal entry valuing the transfer at the
// aggregate cost of the components this SPECIFIC order actually issued
// (SUM(production_order_material_issues.quantity_issued * products.cost_price),
// joined against the material-issue log Start already wrote - not
// re-derived from the BOM's current quantity_per_unit, which could have
// changed since this order started):
//
//   - DR Work in Progress / CR Inventory, for the component value
//   - DR Finished Goods / CR Work in Progress, for the same value
//
// This is simple transfer costing (finished goods valued at the cost of
// materials consumed, no labor/overhead allocation) - deliberately
// minimal, matching this batch's own scope boundary ("the data model and
// operations foundation, not a full MES"); WIP nets to exactly zero after
// completion, which is the correct steady-state balance for a completed
// order with no other order concurrently in progress. If the aggregate
// component value is zero (e.g. no component had a cost_price set), the
// journal entry is skipped entirely (not posted with a zero amount,
// which journal_lines.amount's own CHECK (amount > 0) would reject) -
// the same "skip, not fail" contract this codebase's other optional-data
// bridges (e.g. internal/workflow's resolveInvoice) already establish:
// stock still moves, the order still completes, only the ledger entry is
// skipped when there's no cost data to value it with. Owner-gated.
//
// Atomicity: one transaction, same shape StartProductionOrder's own doc
// comment describes - a lock on the order row, a status check, the stock
// adjustment, the journal posting (if any), and the status flip all
// commit or roll back together.
func CompleteProductionOrder(ctx context.Context, pool *pgxpool.Pool, firmID, userID, orderID uuid.UUID, quantityProduced string) (ProductionOrder, error) {
	if !validPositiveQuantity(quantityProduced) {
		return ProductionOrder{}, fmt.Errorf("%w: quantityProduced %q must be a positive decimal with at most 4 fraction digits", ErrInvalidProductionOrder, quantityProduced)
	}

	var po ProductionOrder
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

		var productID uuid.UUID
		var currentStatus, orderNumber string
		if err := tx.QueryRow(ctx, `
			SELECT product_id, status, order_number FROM production_orders WHERE id = $1 AND firm_id = $2 FOR UPDATE
		`, orderID, firmID).Scan(&productID, &currentStatus, &orderNumber); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrProductionOrderNotFound
			}
			return fmt.Errorf("look up production order: %w", err)
		}
		if ProductionOrderStatus(currentStatus) != ProductionOrderStatusInProgress {
			return fmt.Errorf("%w: cannot complete a production order in status %q", ErrInvalidProductionOrderTransition, currentStatus)
		}

		if err := inventory.AdjustStockTx(ctx, tx, firmID, productID, nil, quantityProduced, finishedGoodsReason, productionOrderSourceType, &orderID); err != nil {
			return err
		}

		var componentValue string
		var hasValue bool
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(i.quantity_issued * COALESCE(p.cost_price, 0)), 0)::numeric(19,4)::text, COALESCE(SUM(i.quantity_issued * COALESCE(p.cost_price, 0)), 0) > 0
			FROM production_order_material_issues i
			JOIN products p ON p.id = i.product_id
			WHERE i.production_order_id = $1 AND i.firm_id = $2
		`, orderID, firmID).Scan(&componentValue, &hasValue); err != nil {
			return fmt.Errorf("sum component value: %w", err)
		}

		if hasValue {
			lines := []accounting.LineInput{
				{AccountCode: accounting.WorkInProgressAccountCode, Side: accounting.SideDebit, Amount: componentValue},
				{AccountCode: accounting.InventoryAccountCode, Side: accounting.SideCredit, Amount: componentValue},
				{AccountCode: accounting.FinishedGoodsAccountCode, Side: accounting.SideDebit, Amount: componentValue},
				{AccountCode: accounting.WorkInProgressAccountCode, Side: accounting.SideCredit, Amount: componentValue},
			}
			description := fmt.Sprintf("Production order %q completed", orderNumber)
			if _, err := accounting.PostJournalEntryTx(ctx, tx, firmID, userID, description, productionOrderSourceType, &orderID, lines); err != nil {
				return err
			}
		}

		row := tx.QueryRow(ctx, `
			UPDATE production_orders SET status = $1, quantity_produced = $2::numeric, actual_end = now()
			WHERE id = $3 AND firm_id = $4
			RETURNING `+productionOrderColumns, string(ProductionOrderStatusCompleted), quantityProduced, orderID, firmID)
		po, err = scanProductionOrderRow(row)
		if err != nil {
			return fmt.Errorf("complete production order: %w", err)
		}
		po.MaterialIssues, err = fetchMaterialIssuesTx(ctx, tx, orderID)
		if err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, orderID, productionOrderAuditEntityType, completeProductionOrderAuditAction, map[string]any{
			"quantityProduced": quantityProduced, "componentValue": componentValue,
		})
	})
	if err != nil {
		return ProductionOrder{}, err
	}
	return po, nil
}
