// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package inventory

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// amountPattern mirrors internal/accounting's own amountPattern - a
// plain positive decimal string, matching this package's own numeric(19,4)
// columns (unit_price/cost_price/min_quantity). Redeclared here rather
// than imported cross-package, same reasoning internal/hr's own
// amountPattern gives.
var amountPattern = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)

// signedDecimalPattern is quantityChange's own pattern (AdjustStockTx
// below) - unlike amountPattern, an optional leading "-" is valid: a
// stock decrease (a sale) is a real negative change, not a positive
// amount paired with a separate "side" column the way journal_lines
// carries direction (there's no natural "credit/debit" equivalent for a
// quantity delta).
var signedDecimalPattern = regexp.MustCompile(`^-?[0-9]{1,15}(\.[0-9]{1,4})?$`)

// postgresCheckViolation is the SQLSTATE code Postgres raises for a CHECK
// constraint violation - stock_levels.quantity's own `CHECK (quantity >=
// 0)` (migrations/0011_inventory_core.up.sql) is AdjustStockTx's last
// line of defense; this lets a violation of that constraint (which
// should never actually fire, since AdjustStockTx checks the resulting
// quantity itself first) still map to the same clean ErrInsufficientStock
// instead of a raw constraint-violation error, if it ever somehow did.
const postgresCheckViolation = "23514"

// StockLevel is one product's on-hand quantity at one location.
type StockLevel struct {
	ProductID        uuid.UUID
	Location         string
	Quantity         string
	ReservedQuantity string
	UpdatedAt        time.Time
}

// AdjustStockTx applies quantityChange (a signed decimal string - see
// signedDecimalPattern) to productID's stock_levels row at location
// within firmID, upserting the row if it doesn't exist yet, and appends
// one immutable stock_movements row recording exactly this change - both
// in the same already-open transaction, so a caller (internal/workflow's
// record_sale/receive hooks, see engine.go) can make this atomic with
// its own state change, the same "WithFirmContext-scoped function called
// from ExecuteTransition" pattern internal/accounting.PostJournalEntryTx
// already establishes for the ledger bridge - not a new mechanism.
//
// If quantityChange would take quantity below zero, this returns
// ErrInsufficientStock and applies nothing - the caller's own transaction
// (already open, shared with the workflow state change) rolls back
// everything together, so a rejected sale never partially decrements
// stock. This is checked in Go BEFORE the write (not just relying on the
// database CHECK constraint to reject it after the fact) so the error
// returned is always the clean, typed ErrInsufficientStock, not a raw
// constraint-violation error - the CHECK constraint itself
// (migrations/0011_inventory_core.up.sql) remains as the actual
// authoritative guarantee, defense in depth exactly like RLS is for
// tenant isolation.
//
// The caller is responsible for its own authorization decision before
// calling this - AdjustStockTx makes none of its own, the same division
// of responsibility PostJournalEntryTx uses: internal/workflow's
// ExecuteTransition has already checked the transition's own permission
// by the time it reaches here.
//
// ciaudit:ignore-firmid-check: shared stock-adjustment primitive; every
// caller (internal/workflow's record_sale/receive hooks) has already run
// its own authorization check before calling this.
func AdjustStockTx(ctx context.Context, tx pgx.Tx, firmID, productID uuid.UUID, location, quantityChange, reason, sourceType string, sourceID *uuid.UUID) error {
	location = strings.TrimSpace(location)
	if location == "" {
		location = "default"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("%w: reason must not be empty", ErrInvalidStockAdjustment)
	}
	if !signedDecimalPattern.MatchString(quantityChange) {
		return fmt.Errorf("%w: quantityChange %q must be a decimal with at most 4 fraction digits", ErrInvalidStockAdjustment, quantityChange)
	}

	// Lock (or create) the target row first, then check what the
	// resulting quantity would be, all before writing it - avoids a
	// race where two concurrent sales of the same product could both
	// read a sufficient quantity before either writes.
	var currentQuantity string
	err := tx.QueryRow(ctx, `
		INSERT INTO stock_levels (firm_id, product_id, location, quantity)
		VALUES ($1, $2, $3, 0)
		ON CONFLICT (firm_id, product_id, location) DO UPDATE SET location = stock_levels.location
		RETURNING quantity::text
	`, firmID, productID, location).Scan(&currentQuantity)
	if err != nil {
		return fmt.Errorf("lock stock level row: %w", err)
	}

	var resultingQuantity string
	if err := tx.QueryRow(ctx, `SELECT ($1::numeric + $2::numeric)::text`, currentQuantity, quantityChange).Scan(&resultingQuantity); err != nil {
		return fmt.Errorf("compute resulting quantity: %w", err)
	}
	if strings.HasPrefix(resultingQuantity, "-") {
		return fmt.Errorf("%w: current %s, change %s", ErrInsufficientStock, currentQuantity, quantityChange)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE stock_levels SET quantity = $1::numeric, updated_at = now()
		WHERE firm_id = $2 AND product_id = $3 AND location = $4
	`, resultingQuantity, firmID, productID, location); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == postgresCheckViolation {
			return fmt.Errorf("%w: current %s, change %s", ErrInsufficientStock, currentQuantity, quantityChange)
		}
		return fmt.Errorf("update stock level: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO stock_movements (firm_id, product_id, location, quantity_change, reason, source_type, source_id)
		VALUES ($1, $2, $3, $4::numeric, $5, NULLIF($6, ''), $7)
	`, firmID, productID, location, quantityChange, reason, sourceType, sourceID); err != nil {
		return fmt.Errorf("insert stock movement: %w", err)
	}

	return nil
}

// GetStock returns productID's stock levels across every location within
// firmID. Member-gated, same tier as ListProducts.
func GetStock(ctx context.Context, pool *pgxpool.Pool, firmID, userID, productID uuid.UUID) ([]StockLevel, error) {
	var levels []StockLevel
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT product_id, location, quantity::text, reserved_quantity::text, updated_at
			FROM stock_levels WHERE product_id = $1
			ORDER BY location
		`, productID)
		if err != nil {
			return fmt.Errorf("get stock: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var l StockLevel
			if err := rows.Scan(&l.ProductID, &l.Location, &l.Quantity, &l.ReservedQuantity, &l.UpdatedAt); err != nil {
				return err
			}
			levels = append(levels, l)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return levels, nil
}

// StockMovement is one immutable stock_movements row.
type StockMovement struct {
	ID             uuid.UUID
	ProductID      uuid.UUID
	Location       string
	QuantityChange string
	Reason         string
	SourceType     string
	SourceID       *uuid.UUID
	CreatedAt      time.Time
}

// ListMovementsOptions controls paging for ListStockMovements - same
// shape as internal/auditlog.ListOptions/internal/accounting.ListOptions
// (zero value returns every entry, unpaged).
type ListMovementsOptions struct {
	Limit  int
	Offset int
	// ProductID, when non-nil, keeps only movements for that product.
	ProductID *uuid.UUID
}

// ListMovementsResult is ListStockMovements' return shape.
type ListMovementsResult struct {
	Movements []StockMovement
	Total     int
}

// ListStockMovements returns firmID's stock_movements rows, most recent
// first. Member-gated, same tier as ListProducts.
func ListStockMovements(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListMovementsOptions) (ListMovementsResult, error) {
	var result ListMovementsResult
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, product_id, location, quantity_change::text, reason, COALESCE(source_type, ''), source_id, created_at, COUNT(*) OVER()
			FROM stock_movements
			WHERE ($1::uuid IS NULL OR product_id = $1)
			ORDER BY created_at DESC, id DESC
			LIMIT NULLIF($2, 0) OFFSET $3
		`, opts.ProductID, opts.Limit, opts.Offset)
		if err != nil {
			return fmt.Errorf("list stock movements: %w", err)
		}
		defer rows.Close()
		var movements []StockMovement
		for rows.Next() {
			var m StockMovement
			var total int
			if err := rows.Scan(&m.ID, &m.ProductID, &m.Location, &m.QuantityChange, &m.Reason, &m.SourceType, &m.SourceID, &m.CreatedAt, &total); err != nil {
				return err
			}
			movements = append(movements, m)
			result.Total = total
		}
		result.Movements = movements
		return rows.Err()
	})
	if err != nil {
		return ListMovementsResult{}, err
	}
	return result, nil
}
