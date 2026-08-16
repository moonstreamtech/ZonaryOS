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

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const transferAuditEntityType = "inventory_transfer"

const (
	createTransferAuditAction   = "create"
	completeTransferAuditAction = "complete"
	cancelTransferAuditAction   = "cancel"
)

// transferQuantityPattern mirrors inventory_transfers.quantity's own
// numeric(10,4) CHECK (quantity > 0) - a strictly positive decimal,
// redeclared here rather than imported cross-package, same reasoning
// every other package's own copy of this pattern gives.
var transferQuantityPattern = regexp.MustCompile(`^[0-9]{1,6}(\.[0-9]{1,4})?$`)

// TransferStatus is inventory_transfers.status's closed vocabulary -
// mirrors migrations/0028_warehouse_management.up.sql's own CHECK
// constraint.
type TransferStatus string

const (
	TransferStatusDraft     TransferStatus = "draft"
	TransferStatusInTransit TransferStatus = "in_transit"
	TransferStatusCompleted TransferStatus = "completed"
	TransferStatusCancelled TransferStatus = "cancelled"
	transferOutReason                      = "transfer_out"
	transferInReason                       = "transfer_in"
	transferSourceType                     = "inventory_transfer"
)

// Transfer is one inventory_transfers row.
type Transfer struct {
	ID             uuid.UUID
	FirmID         uuid.UUID
	FromLocationID uuid.UUID
	ToLocationID   uuid.UUID
	ProductID      uuid.UUID
	Quantity       string
	Status         TransferStatus
	Notes          *string
	CreatedAt      time.Time
}

const transferColumns = `id, firm_id, from_location_id, to_location_id, product_id, quantity::text, status, notes, created_at`

func scanTransferRow(row pgx.Row) (Transfer, error) {
	var t Transfer
	var status string
	if err := row.Scan(&t.ID, &t.FirmID, &t.FromLocationID, &t.ToLocationID, &t.ProductID, &t.Quantity, &status, &t.Notes, &t.CreatedAt); err != nil {
		return Transfer{}, err
	}
	t.Status = TransferStatus(status)
	return t, nil
}

// CreateTransferInput is CreateTransfer's request shape.
type CreateTransferInput struct {
	FromLocationID uuid.UUID
	ToLocationID   uuid.UUID
	ProductID      uuid.UUID
	Quantity       string
	Notes          string
}

// CreateTransfer records a draft transfer of Quantity units of ProductID
// from FromLocationID to ToLocationID within firmID - no stock actually
// moves yet (that's CompleteTransfer's job); this only validates the
// request shape and that both locations exist in firmID. Owner-gated.
func CreateTransfer(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateTransferInput) (Transfer, error) {
	if input.FromLocationID == input.ToLocationID {
		return Transfer{}, fmt.Errorf("%w: from and to locations must differ", ErrInvalidTransfer)
	}
	if !transferQuantityPattern.MatchString(strings.TrimSpace(input.Quantity)) {
		return Transfer{}, fmt.Errorf("%w: quantity %q must be a positive decimal with at most 4 fraction digits", ErrInvalidTransfer, input.Quantity)
	}

	var t Transfer
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

		for _, locationID := range []uuid.UUID{input.FromLocationID, input.ToLocationID} {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouse_locations WHERE id = $1 AND firm_id = $2)`, locationID, firmID).Scan(&exists); err != nil {
				return fmt.Errorf("look up location: %w", err)
			}
			if !exists {
				return fmt.Errorf("%w: location %s not found", ErrInvalidLocation, locationID)
			}
		}

		notes := optionalString(input.Notes)
		row := tx.QueryRow(ctx, `
			INSERT INTO inventory_transfers (firm_id, from_location_id, to_location_id, product_id, quantity, notes)
			VALUES ($1, $2, $3, $4, $5::numeric, $6)
			RETURNING `+transferColumns,
			firmID, input.FromLocationID, input.ToLocationID, input.ProductID, input.Quantity, notes)
		t, err = scanTransferRow(row)
		if err != nil {
			return fmt.Errorf("insert transfer: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, t.ID, transferAuditEntityType, createTransferAuditAction, map[string]any{
			"productId": t.ProductID, "quantity": t.Quantity,
		})
	})
	if err != nil {
		return Transfer{}, err
	}
	return t, nil
}

// ListTransfers returns firmID's inventory transfers, newest first.
// Member-gated.
func ListTransfers(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Transfer, error) {
	var transfers []Transfer
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+transferColumns+` FROM inventory_transfers WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list transfers: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTransferRow(rows)
			if err != nil {
				return err
			}
			transfers = append(transfers, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return transfers, nil
}

// GetTransfer resolves one transfer by id within firmID. Member-gated.
func GetTransfer(ctx context.Context, pool *pgxpool.Pool, firmID, userID, transferID uuid.UUID) (Transfer, error) {
	var t Transfer
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+transferColumns+` FROM inventory_transfers WHERE id = $1 AND firm_id = $2`, transferID, firmID)
		t, err = scanTransferRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTransferNotFound
		}
		return err
	})
	if err != nil {
		return Transfer{}, err
	}
	return t, nil
}

// CompleteTransfer atomically deducts Quantity from the transfer's
// from_location and adds it to the to_location: one transaction, a lock
// on the transfer row, a status check, two AdjustStockTx calls (which
// each append their own stock_movements row - "transfer_out" at the
// source, "transfer_in" at the destination, both tagged back to this
// transfer via source_type/source_id), and the status flip - the same
// atomic, fail-closed shape internal/manufacturing.StartProductionOrder's
// own doc comment describes. If the source location doesn't have enough
// stock, AdjustStockTx's own ErrInsufficientStock propagates and the
// whole transaction rolls back - the transfer stays in its prior status,
// nothing moves. Owner-gated.
func CompleteTransfer(ctx context.Context, pool *pgxpool.Pool, firmID, userID, transferID uuid.UUID) (Transfer, error) {
	var t Transfer
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

		row := tx.QueryRow(ctx, `SELECT `+transferColumns+` FROM inventory_transfers WHERE id = $1 AND firm_id = $2 FOR UPDATE`, transferID, firmID)
		t, err = scanTransferRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTransferNotFound
		}
		if err != nil {
			return fmt.Errorf("look up transfer: %w", err)
		}
		if t.Status != TransferStatusDraft && t.Status != TransferStatusInTransit {
			return fmt.Errorf("%w: cannot complete a transfer in status %q", ErrInvalidTransferTransition, t.Status)
		}

		if err := inventory.AdjustStockTx(ctx, tx, firmID, t.ProductID, &t.FromLocationID, "-"+t.Quantity, transferOutReason, transferSourceType, &transferID); err != nil {
			return err
		}
		if err := inventory.AdjustStockTx(ctx, tx, firmID, t.ProductID, &t.ToLocationID, t.Quantity, transferInReason, transferSourceType, &transferID); err != nil {
			return err
		}

		row = tx.QueryRow(ctx, `
			UPDATE inventory_transfers SET status = $1 WHERE id = $2 AND firm_id = $3
			RETURNING `+transferColumns, string(TransferStatusCompleted), transferID, firmID)
		t, err = scanTransferRow(row)
		if err != nil {
			return fmt.Errorf("complete transfer: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, transferID, transferAuditEntityType, completeTransferAuditAction, map[string]any{"quantity": t.Quantity})
	})
	if err != nil {
		return Transfer{}, err
	}
	return t, nil
}

// CancelTransfer moves a draft/in_transit transfer to cancelled - no
// stock ever moved for it, so there's nothing to reverse. Owner-gated.
func CancelTransfer(ctx context.Context, pool *pgxpool.Pool, firmID, userID, transferID uuid.UUID) (Transfer, error) {
	var t Transfer
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
		if err := tx.QueryRow(ctx, `SELECT status FROM inventory_transfers WHERE id = $1 AND firm_id = $2 FOR UPDATE`, transferID, firmID).Scan(&currentStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTransferNotFound
			}
			return fmt.Errorf("look up transfer: %w", err)
		}
		if TransferStatus(currentStatus) != TransferStatusDraft && TransferStatus(currentStatus) != TransferStatusInTransit {
			return fmt.Errorf("%w: cannot cancel a transfer in status %q", ErrInvalidTransferTransition, currentStatus)
		}

		row := tx.QueryRow(ctx, `
			UPDATE inventory_transfers SET status = $1 WHERE id = $2 AND firm_id = $3
			RETURNING `+transferColumns, string(TransferStatusCancelled), transferID, firmID)
		t, err = scanTransferRow(row)
		if err != nil {
			return fmt.Errorf("cancel transfer: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, transferID, transferAuditEntityType, cancelTransferAuditAction, map[string]any{})
	})
	if err != nil {
		return Transfer{}, err
	}
	return t, nil
}
