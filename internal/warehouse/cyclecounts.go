// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package warehouse

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

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const cycleCountAuditEntityType = "cycle_count_session"

const (
	createCycleCountAuditAction   = "create"
	completeCycleCountAuditAction = "complete"
)

// countedQuantityPattern is RecordCount's own input shape - a
// non-negative decimal (a physical count of zero is a real, valid
// result, unlike inventory_transfers.quantity which must be strictly
// positive).
var countedQuantityPattern = regexp.MustCompile(`^[0-9]{1,6}(\.[0-9]{1,4})?$`)

const cycleCountAdjustmentReason = "cycle_count"
const cycleCountSourceType = "cycle_count_session"

// CycleCountSessionStatus is cycle_count_sessions.status's closed
// vocabulary - mirrors migrations/0028_warehouse_management.up.sql's own
// CHECK constraint.
type CycleCountSessionStatus string

const (
	CycleCountSessionStatusOpen      CycleCountSessionStatus = "open"
	CycleCountSessionStatusCompleted CycleCountSessionStatus = "completed"
)

// CycleCountLine is one cycle_count_lines row. Variance is nil until
// CountedQuantity is set (variance's own GENERATED ALWAYS expression is
// NULL when counted_quantity is NULL).
type CycleCountLine struct {
	ID              uuid.UUID
	SessionID       uuid.UUID
	LocationID      uuid.UUID
	ProductID       uuid.UUID
	SystemQuantity  string
	CountedQuantity *string
	Variance        *string
	Adjusted        bool
}

// CycleCountSession is one cycle_count_sessions row, with its lines.
type CycleCountSession struct {
	ID          uuid.UUID
	FirmID      uuid.UUID
	WarehouseID uuid.UUID
	Status      CycleCountSessionStatus
	CreatedAt   time.Time
	Lines       []CycleCountLine
}

// CycleCountLineInput is one requested (location, product) pair to
// snapshot on CreateCycleCountSession.
type CycleCountLineInput struct {
	LocationID uuid.UUID
	ProductID  uuid.UUID
}

// CreateCycleCountSessionInput is CreateCycleCountSession's request
// shape.
type CreateCycleCountSessionInput struct {
	WarehouseID uuid.UUID
	Lines       []CycleCountLineInput
}

// CreateCycleCountSession opens a new session for warehouseID and
// snapshots SystemQuantity for every requested (location, product) pair
// from the CURRENT stock_levels.quantity (0 if there's no row yet) -
// system_quantity is captured once, at session creation, exactly the way
// a physical count needs a fixed baseline to compare against; it does
// NOT track further stock movement while the count is in progress.
// Owner-gated.
func CreateCycleCountSession(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateCycleCountSessionInput) (CycleCountSession, error) {
	if len(input.Lines) == 0 {
		return CycleCountSession{}, fmt.Errorf("%w: a cycle count session must have at least one line", ErrInvalidCycleCount)
	}

	var s CycleCountSession
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

		var warehouseExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouses WHERE id = $1 AND firm_id = $2)`, input.WarehouseID, firmID).Scan(&warehouseExists); err != nil {
			return fmt.Errorf("look up warehouse: %w", err)
		}
		if !warehouseExists {
			return ErrWarehouseNotFound
		}

		s.FirmID = firmID
		s.WarehouseID = input.WarehouseID
		s.Status = CycleCountSessionStatusOpen
		if err := tx.QueryRow(ctx, `
			INSERT INTO cycle_count_sessions (firm_id, warehouse_id) VALUES ($1, $2) RETURNING id, created_at
		`, firmID, input.WarehouseID).Scan(&s.ID, &s.CreatedAt); err != nil {
			return fmt.Errorf("insert cycle count session: %w", err)
		}

		for _, l := range input.Lines {
			var locationWarehouseID uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT warehouse_id FROM warehouse_locations WHERE id = $1 AND firm_id = $2`, l.LocationID, firmID).Scan(&locationWarehouseID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("%w: location %s not found", ErrInvalidLocation, l.LocationID)
				}
				return fmt.Errorf("look up location: %w", err)
			}
			if locationWarehouseID != input.WarehouseID {
				return fmt.Errorf("%w: location %s does not belong to this warehouse", ErrInvalidLocation, l.LocationID)
			}

			var line CycleCountLine
			if err := tx.QueryRow(ctx, `
				INSERT INTO cycle_count_lines (firm_id, session_id, location_id, product_id, system_quantity)
				SELECT $1, $2, $3, $4, COALESCE((SELECT quantity FROM stock_levels WHERE firm_id = $1 AND product_id = $4 AND location_id = $3), 0)
				RETURNING id, session_id, location_id, product_id, system_quantity::text, counted_quantity::text, variance::text, adjusted
			`, firmID, s.ID, l.LocationID, l.ProductID).Scan(
				&line.ID, &line.SessionID, &line.LocationID, &line.ProductID, &line.SystemQuantity, &line.CountedQuantity, &line.Variance, &line.Adjusted,
			); err != nil {
				return fmt.Errorf("insert cycle count line: %w", err)
			}
			s.Lines = append(s.Lines, line)
		}

		return auditlog.Write(ctx, tx, firmID, userID, s.ID, cycleCountAuditEntityType, createCycleCountAuditAction, map[string]any{"warehouseId": s.WarehouseID, "lines": len(s.Lines)})
	})
	if err != nil {
		return CycleCountSession{}, err
	}
	return s, nil
}

func fetchCycleCountLinesTx(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) ([]CycleCountLine, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, session_id, location_id, product_id, system_quantity::text, counted_quantity::text, variance::text, adjusted
		FROM cycle_count_lines WHERE session_id = $1 ORDER BY id
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list cycle count lines: %w", err)
	}
	defer rows.Close()
	var lines []CycleCountLine
	for rows.Next() {
		var l CycleCountLine
		if err := rows.Scan(&l.ID, &l.SessionID, &l.LocationID, &l.ProductID, &l.SystemQuantity, &l.CountedQuantity, &l.Variance, &l.Adjusted); err != nil {
			return nil, err
		}
		lines = append(lines, l)
	}
	return lines, rows.Err()
}

func scanSessionRow(row pgx.Row) (CycleCountSession, error) {
	var s CycleCountSession
	var status string
	if err := row.Scan(&s.ID, &s.FirmID, &s.WarehouseID, &status, &s.CreatedAt); err != nil {
		return CycleCountSession{}, err
	}
	s.Status = CycleCountSessionStatus(status)
	return s, nil
}

const sessionColumns = `id, firm_id, warehouse_id, status, created_at`

// ListCycleCountSessions returns firmID's cycle count sessions (without
// their lines - callers needing line detail use GetCycleCountSession),
// newest first. Member-gated.
func ListCycleCountSessions(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]CycleCountSession, error) {
	var sessions []CycleCountSession
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+sessionColumns+` FROM cycle_count_sessions WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list cycle count sessions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			s, err := scanSessionRow(rows)
			if err != nil {
				return err
			}
			sessions = append(sessions, s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

// GetCycleCountSession resolves one session (with its lines) by id
// within firmID. Member-gated.
func GetCycleCountSession(ctx context.Context, pool *pgxpool.Pool, firmID, userID, sessionID uuid.UUID) (CycleCountSession, error) {
	var s CycleCountSession
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+sessionColumns+` FROM cycle_count_sessions WHERE id = $1 AND firm_id = $2`, sessionID, firmID)
		s, err = scanSessionRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCycleCountSessionNotFound
		}
		if err != nil {
			return fmt.Errorf("look up cycle count session: %w", err)
		}
		s.Lines, err = fetchCycleCountLinesTx(ctx, tx, sessionID)
		return err
	})
	if err != nil {
		return CycleCountSession{}, err
	}
	return s, nil
}

// RecordCount fills in lineID's CountedQuantity - the "scan/enter product
// + location + counted qty" step. Only valid while the owning session is
// still 'open'. Owner-gated.
func RecordCount(ctx context.Context, pool *pgxpool.Pool, firmID, userID, sessionID, lineID uuid.UUID, countedQuantity string) (CycleCountLine, error) {
	countedQuantity = strings.TrimSpace(countedQuantity)
	if !countedQuantityPattern.MatchString(countedQuantity) {
		return CycleCountLine{}, fmt.Errorf("%w: countedQuantity %q must be a non-negative decimal with at most 4 fraction digits", ErrInvalidCycleCount, countedQuantity)
	}

	var line CycleCountLine
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

		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM cycle_count_sessions WHERE id = $1 AND firm_id = $2`, sessionID, firmID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrCycleCountSessionNotFound
			}
			return fmt.Errorf("look up cycle count session: %w", err)
		}
		if CycleCountSessionStatus(status) != CycleCountSessionStatusOpen {
			return fmt.Errorf("%w: cannot record a count on a session in status %q", ErrInvalidCycleCountTransition, status)
		}

		if err := tx.QueryRow(ctx, `
			UPDATE cycle_count_lines SET counted_quantity = $1::numeric
			WHERE id = $2 AND session_id = $3 AND firm_id = $4
			RETURNING id, session_id, location_id, product_id, system_quantity::text, counted_quantity::text, variance::text, adjusted
		`, countedQuantity, lineID, sessionID, firmID).Scan(
			&line.ID, &line.SessionID, &line.LocationID, &line.ProductID, &line.SystemQuantity, &line.CountedQuantity, &line.Variance, &line.Adjusted,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrCycleCountLineNotFound
			}
			return fmt.Errorf("record count: %w", err)
		}
		return nil
	})
	if err != nil {
		return CycleCountLine{}, err
	}
	return line, nil
}

// CompleteCycleCountSession closes sessionID. When adjust is true, every
// line whose CountedQuantity has been recorded and whose Variance is
// non-zero gets: (1) a real stock adjustment via
// internal/inventory.AdjustStockTx (reason "cycle_count"), and (2) is
// flagged Adjusted - both inside the same transaction the session's own
// status flip commits in, so a partial adjustment (some lines posted,
// others not, or stock moved but the session left 'open') can never
// happen. The lines' combined value (variance * each product's
// cost_price, net across the whole session - a firm doing a full
// warehouse recount in one session gets one journal entry, not one per
// line) is posted as a single balanced entry: a net INCREASE (more found
// than expected) debits Inventory / credits Inventory Adjustment; a net
// DECREASE (shrinkage) debits Inventory Adjustment / credits Inventory -
// skipped entirely if the net value is exactly zero (stock still moves
// either way; only the ledger entry is conditional on there being
// something to value it with, the same judgment call
// internal/manufacturing.CompleteProductionOrder's own doc comment
// explains for a component value of zero). Owner-gated.
func CompleteCycleCountSession(ctx context.Context, pool *pgxpool.Pool, firmID, userID, sessionID uuid.UUID, adjust bool) (CycleCountSession, error) {
	var s CycleCountSession
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

		row := tx.QueryRow(ctx, `SELECT `+sessionColumns+` FROM cycle_count_sessions WHERE id = $1 AND firm_id = $2 FOR UPDATE`, sessionID, firmID)
		s, err = scanSessionRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCycleCountSessionNotFound
		}
		if err != nil {
			return fmt.Errorf("look up cycle count session: %w", err)
		}
		if s.Status != CycleCountSessionStatusOpen {
			return fmt.Errorf("%w: cannot complete a session in status %q", ErrInvalidCycleCountTransition, s.Status)
		}

		if adjust {
			rows, err := tx.Query(ctx, `
				SELECT id, location_id, product_id, variance::text
				FROM cycle_count_lines
				WHERE session_id = $1 AND firm_id = $2 AND counted_quantity IS NOT NULL AND variance <> 0
				FOR UPDATE
			`, sessionID, firmID)
			if err != nil {
				return fmt.Errorf("list adjustable cycle count lines: %w", err)
			}
			type adjustableLine struct {
				id, locationID, productID uuid.UUID
				variance                  string
			}
			var toAdjust []adjustableLine
			for rows.Next() {
				var a adjustableLine
				if err := rows.Scan(&a.id, &a.locationID, &a.productID, &a.variance); err != nil {
					rows.Close()
					return err
				}
				toAdjust = append(toAdjust, a)
			}
			if err := rows.Err(); err != nil {
				return err
			}
			rows.Close()

			for _, a := range toAdjust {
				if err := inventory.AdjustStockTx(ctx, tx, firmID, a.productID, &a.locationID, a.variance, cycleCountAdjustmentReason, cycleCountSourceType, &sessionID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE cycle_count_lines SET adjusted = true WHERE id = $1`, a.id); err != nil {
					return fmt.Errorf("flag cycle count line adjusted: %w", err)
				}
			}

			var totalValue string
			var hasValue, isNegative bool
			if err := tx.QueryRow(ctx, `
				SELECT
					COALESCE(SUM(l.variance * COALESCE(p.cost_price, 0)), 0)::numeric(19,4)::text,
					COALESCE(SUM(l.variance * COALESCE(p.cost_price, 0)), 0) <> 0,
					COALESCE(SUM(l.variance * COALESCE(p.cost_price, 0)), 0) < 0
				FROM cycle_count_lines l
				JOIN products p ON p.id = l.product_id
				WHERE l.session_id = $1 AND l.firm_id = $2 AND l.adjusted
			`, sessionID, firmID).Scan(&totalValue, &hasValue, &isNegative); err != nil {
				return fmt.Errorf("sum cycle count adjustment value: %w", err)
			}

			if hasValue {
				absValue := strings.TrimPrefix(totalValue, "-")
				var lines []accounting.LineInput
				if isNegative {
					lines = []accounting.LineInput{
						{AccountCode: accounting.InventoryAdjustmentAccountCode, Side: accounting.SideDebit, Amount: absValue},
						{AccountCode: accounting.InventoryAccountCode, Side: accounting.SideCredit, Amount: absValue},
					}
				} else {
					lines = []accounting.LineInput{
						{AccountCode: accounting.InventoryAccountCode, Side: accounting.SideDebit, Amount: absValue},
						{AccountCode: accounting.InventoryAdjustmentAccountCode, Side: accounting.SideCredit, Amount: absValue},
					}
				}
				description := "Cycle count adjustment"
				if _, err := accounting.PostJournalEntryTx(ctx, tx, firmID, userID, description, cycleCountSourceType, &sessionID, lines); err != nil {
					return err
				}
			}
		}

		row = tx.QueryRow(ctx, `
			UPDATE cycle_count_sessions SET status = $1 WHERE id = $2 AND firm_id = $3
			RETURNING `+sessionColumns, string(CycleCountSessionStatusCompleted), sessionID, firmID)
		s, err = scanSessionRow(row)
		if err != nil {
			return fmt.Errorf("complete cycle count session: %w", err)
		}
		s.Lines, err = fetchCycleCountLinesTx(ctx, tx, sessionID)
		if err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, sessionID, cycleCountAuditEntityType, completeCycleCountAuditAction, map[string]any{"adjust": adjust})
	})
	if err != nil {
		return CycleCountSession{}, err
	}
	return s, nil
}
