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
	ProductID uuid.UUID
	LocationID uuid.UUID
	// LocationCode is warehouse_locations.code, joined in for display -
	// StockLevel/StockMovement's own reader doesn't need a second round
	// trip to internal/warehouse just to show which location a row
	// belongs to.
	LocationCode     string
	Quantity         string
	ReservedQuantity string
	UpdatedAt        time.Time
}

// defaultLocationIDTx resolves firmID's default warehouse location
// (warehouse_locations.is_default) - what AdjustStockTx falls back to
// when a caller (internal/workflow's record_sale/receive hooks,
// internal/manufacturing's material issue/completion) has no specific
// location of its own to pass. Every firm has exactly one (the
// migrations/0028_warehouse_management.up.sql backfill seeds one for
// every existing firm unconditionally, and internal/wizard.CreateDefaultFirm
// does the same for every new firm), so ErrNoDefaultLocation should never
// actually surface outside a firm created by some path that skipped that
// seeding step.
//
// internal/inventory intentionally does NOT import internal/warehouse
// here (that would be the wrong direction: warehouse locations are a
// structural concept AdjustStockTx merely references by id, the same way
// this package already references products/firms by id without owning
// them) - this is a plain query against warehouse_locations, not a call
// into that package's API.
//
// ciaudit:ignore-firmid-check: internal helper, only called by
// AdjustStockTx, which makes no authorization decision of its own either
// (see AdjustStockTx's own doc comment) - every caller has already run
// its own permission check before either function is reached.
func defaultLocationIDTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM warehouse_locations WHERE firm_id = $1 AND is_default LIMIT 1`, firmID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.UUID{}, ErrNoDefaultLocation
		}
		return uuid.UUID{}, fmt.Errorf("resolve default location: %w", err)
	}
	return id, nil
}

// AdjustStockTx applies quantityChange (a signed decimal string - see
// signedDecimalPattern) to productID's stock_levels row at locationID
// within firmID, upserting the row if it doesn't exist yet, and appends
// one immutable stock_movements row recording exactly this change - both
// in the same already-open transaction, so a caller (internal/workflow's
// record_sale/receive hooks, see engine.go) can make this atomic with
// its own state change, the same "WithFirmContext-scoped function called
// from ExecuteTransition" pattern internal/accounting.PostJournalEntryTx
// already establishes for the ledger bridge - not a new mechanism.
//
// locationID may be nil - resolved to firmID's default warehouse
// location (defaultLocationIDTx) - for callers with no specific location
// of their own in context, the same role the old empty-string "location"
// parameter played before warehouse_locations existed.
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
func AdjustStockTx(ctx context.Context, tx pgx.Tx, firmID, productID uuid.UUID, locationID *uuid.UUID, quantityChange, reason, sourceType string, sourceID *uuid.UUID) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("%w: reason must not be empty", ErrInvalidStockAdjustment)
	}
	if !signedDecimalPattern.MatchString(quantityChange) {
		return fmt.Errorf("%w: quantityChange %q must be a decimal with at most 4 fraction digits", ErrInvalidStockAdjustment, quantityChange)
	}

	var resolvedLocationID uuid.UUID
	if locationID != nil {
		resolvedLocationID = *locationID
	} else {
		var err error
		resolvedLocationID, err = defaultLocationIDTx(ctx, tx, firmID)
		if err != nil {
			return err
		}
	}

	// Lock (or create) the target row first, then check what the
	// resulting quantity would be, all before writing it - avoids a
	// race where two concurrent sales of the same product could both
	// read a sufficient quantity before either writes.
	var currentQuantity string
	err := tx.QueryRow(ctx, `
		INSERT INTO stock_levels (firm_id, product_id, location_id, quantity)
		VALUES ($1, $2, $3, 0)
		ON CONFLICT (firm_id, product_id, location_id) DO UPDATE SET location_id = stock_levels.location_id
		RETURNING quantity::text
	`, firmID, productID, resolvedLocationID).Scan(&currentQuantity)
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
		WHERE firm_id = $2 AND product_id = $3 AND location_id = $4
	`, resultingQuantity, firmID, productID, resolvedLocationID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == postgresCheckViolation {
			return fmt.Errorf("%w: current %s, change %s", ErrInsufficientStock, currentQuantity, quantityChange)
		}
		return fmt.Errorf("update stock level: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO stock_movements (firm_id, product_id, location_id, quantity_change, reason, source_type, source_id)
		VALUES ($1, $2, $3, $4::numeric, $5, NULLIF($6, ''), $7)
	`, firmID, productID, resolvedLocationID, quantityChange, reason, sourceType, sourceID); err != nil {
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
			SELECT sl.product_id, sl.location_id, wl.code, sl.quantity::text, sl.reserved_quantity::text, sl.updated_at
			FROM stock_levels sl
			JOIN warehouse_locations wl ON wl.id = sl.location_id
			WHERE sl.product_id = $1
			ORDER BY wl.code
		`, productID)
		if err != nil {
			return fmt.Errorf("get stock: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var l StockLevel
			if err := rows.Scan(&l.ProductID, &l.LocationID, &l.LocationCode, &l.Quantity, &l.ReservedQuantity, &l.UpdatedAt); err != nil {
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

// ListStockForFirm returns every stock_levels row for firmID in one
// query - the batch counterpart to GetStock (single product), added so
// callers that need every product's stock at once (internal/portability.
// ExportFirm) don't loop GetStock per product (an N+1: one query per
// product instead of one query for the whole firm). Member-gated, same
// tier as GetStock/ListProducts.
func ListStockForFirm(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]StockLevel, error) {
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
			SELECT sl.product_id, sl.location_id, wl.code, sl.quantity::text, sl.reserved_quantity::text, sl.updated_at
			FROM stock_levels sl
			JOIN warehouse_locations wl ON wl.id = sl.location_id
			WHERE sl.firm_id = $1
			ORDER BY sl.product_id, wl.code
		`, firmID)
		if err != nil {
			return fmt.Errorf("list stock for firm: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var l StockLevel
			if err := rows.Scan(&l.ProductID, &l.LocationID, &l.LocationCode, &l.Quantity, &l.ReservedQuantity, &l.UpdatedAt); err != nil {
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
	ID         uuid.UUID
	ProductID  uuid.UUID
	LocationID uuid.UUID
	// LocationCode - see StockLevel.LocationCode's own doc comment.
	LocationCode   string
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
			SELECT sm.id, sm.product_id, sm.location_id, wl.code, sm.quantity_change::text, sm.reason, COALESCE(sm.source_type, ''), sm.source_id, sm.created_at, COUNT(*) OVER()
			FROM stock_movements sm
			JOIN warehouse_locations wl ON wl.id = sm.location_id
			WHERE ($1::uuid IS NULL OR sm.product_id = $1)
			ORDER BY sm.created_at DESC, sm.id DESC
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
			if err := rows.Scan(&m.ID, &m.ProductID, &m.LocationID, &m.LocationCode, &m.QuantityChange, &m.Reason, &m.SourceType, &m.SourceID, &m.CreatedAt, &total); err != nil {
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
