// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package budget

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const budgetLineAuditEntityType = "budget_line"

// BudgetLine is one budget_lines row.
type BudgetLine struct {
	ID            uuid.UUID
	FirmID        uuid.UUID
	BudgetID      uuid.UUID
	AccountID     uuid.UUID
	PlannedAmount string
	Notes         *string
}

// CreateBudgetLineInput is CreateBudgetLine's request shape.
type CreateBudgetLineInput struct {
	AccountID     uuid.UUID
	PlannedAmount string
	Notes         string
}

// CreateBudgetLine adds a planned-amount line for one account to
// budgetID within firmID. Owner-gated. AccountID must reference a real
// accounts row in the same firm - checked here (raw SQL against the
// accounts table directly, no internal/accounting import needed for a
// plain existence check) rather than relying solely on budget_lines' own
// FK (which only guarantees the account exists somewhere, not that it's
// in this firm).
func CreateBudgetLine(ctx context.Context, pool *pgxpool.Pool, firmID, userID, budgetID uuid.UUID, input CreateBudgetLineInput) (BudgetLine, error) {
	plannedAmount := strings.TrimSpace(input.PlannedAmount)
	if plannedAmount == "" {
		return BudgetLine{}, fmt.Errorf("%w: planned_amount must not be empty", ErrInvalidBudgetLine)
	}

	var l BudgetLine
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

		var budgetExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM budgets WHERE id = $1 AND firm_id = $2)`, budgetID, firmID).Scan(&budgetExists); err != nil {
			return fmt.Errorf("check budget exists: %w", err)
		}
		if !budgetExists {
			return ErrBudgetNotFound
		}

		var accountExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id = $1 AND firm_id = $2)`, input.AccountID, firmID).Scan(&accountExists); err != nil {
			return fmt.Errorf("check account exists: %w", err)
		}
		if !accountExists {
			return fmt.Errorf("%w: account_id does not belong to this firm", ErrInvalidBudgetLine)
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO budget_lines (firm_id, budget_id, account_id, planned_amount, notes)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, firm_id, budget_id, account_id, planned_amount::text, notes
		`, firmID, budgetID, input.AccountID, plannedAmount, optionalString(input.Notes))
		var scanErr error
		l, scanErr = scanBudgetLineRow(row)
		if scanErr != nil {
			var pgErr *pgconn.PgError
			if errors.As(scanErr, &pgErr) && pgErr.Code == postgresUniqueViolation {
				return ErrDuplicateBudgetLineAccount
			}
			return fmt.Errorf("insert budget line: %w", scanErr)
		}

		return auditlog.Write(ctx, tx, firmID, userID, l.ID, budgetLineAuditEntityType, createAuditAction, map[string]any{"budgetId": budgetID, "accountId": input.AccountID})
	})
	if err != nil {
		return BudgetLine{}, err
	}
	return l, nil
}

// ListBudgetLines returns budgetID's lines within firmID. Member-gated.
func ListBudgetLines(ctx context.Context, pool *pgxpool.Pool, firmID, userID, budgetID uuid.UUID) ([]BudgetLine, error) {
	var lines []BudgetLine
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, budget_id, account_id, planned_amount::text, notes
			FROM budget_lines WHERE firm_id = $1 AND budget_id = $2
		`, firmID, budgetID)
		if err != nil {
			return fmt.Errorf("list budget lines: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			l, err := scanBudgetLineRow(rows)
			if err != nil {
				return err
			}
			lines = append(lines, l)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return lines, nil
}

func scanBudgetLineRow(row rowScanner) (BudgetLine, error) {
	var l BudgetLine
	if err := row.Scan(&l.ID, &l.FirmID, &l.BudgetID, &l.AccountID, &l.PlannedAmount, &l.Notes); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BudgetLine{}, ErrBudgetLineNotFound
		}
		return BudgetLine{}, err
	}
	return l, nil
}
