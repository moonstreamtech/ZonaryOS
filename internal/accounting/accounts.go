// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package accounting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// postgresUniqueViolation is the SQLSTATE code Postgres raises for a
// UNIQUE constraint violation - same constant, same reasoning, as
// internal/workflow.postgresUniqueViolation (redeclared, not imported, to
// avoid a cross-package dependency purely for one string constant).
const postgresUniqueViolation = "23505"

const accountAuditEntityType = "account"

const (
	createAccountAuditAction = "create"
	updateAccountAuditAction = "update"
)

// AccountType is an account's place in the accounting equation - a closed
// set of five values, validated in Go (not a DB CHECK/enum), the same
// "text column, application-validated" convention
// migrations/0008_workflow_rules.up.sql already uses for
// workflow_rules.trigger.
type AccountType string

const (
	AccountTypeAsset     AccountType = "asset"
	AccountTypeLiability AccountType = "liability"
	AccountTypeEquity    AccountType = "equity"
	AccountTypeRevenue   AccountType = "revenue"
	AccountTypeExpense   AccountType = "expense"
)

// normalDebitBalance reports whether at is normally increased by a debit
// (assets and expenses) rather than a credit (liabilities, equity,
// revenue) - GetAccountBalance's sign convention.
func (at AccountType) normalDebitBalance() bool {
	return at == AccountTypeAsset || at == AccountTypeExpense
}

func (at AccountType) valid() bool {
	switch at {
	case AccountTypeAsset, AccountTypeLiability, AccountTypeEquity, AccountTypeRevenue, AccountTypeExpense:
		return true
	default:
		return false
	}
}

// Account is one chart-of-accounts row.
type Account struct {
	ID        uuid.UUID
	FirmID    uuid.UUID
	Code      string
	Name      string
	Type      AccountType
	ParentID  *uuid.UUID
	Currency  string
	IsActive  bool
	CreatedAt time.Time
}

// defaultCurrency is what CreateAccount/PostJournalEntry fall back to when
// no currency is given - Vision's scope boundary for this batch: no
// multi-currency conversion, just a stored field (see this package's doc
// comment).
const defaultCurrency = "TRY"

// CreateAccount adds a new chart-of-accounts row to firmID. Owner-gated:
// defining the accounts a firm's ledger can post to is a structural firm
// decision, the same tier as defining a workflow
// (internal/workflow.DefineWorkflowForFirm) or creating a role
// (internal/permission.CreateRole).
func CreateAccount(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, code, name string, accType AccountType, parentID *uuid.UUID, currency string) (Account, error) {
	code = strings.TrimSpace(code)
	name = strings.TrimSpace(name)
	currency = strings.TrimSpace(currency)
	if currency == "" {
		currency = defaultCurrency
	}
	if code == "" || name == "" || !accType.valid() {
		return Account{}, ErrInvalidAccount
	}

	var account Account
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

		if parentID != nil {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id = $1 AND firm_id = $2)`, *parentID, firmID).Scan(&exists); err != nil {
				return fmt.Errorf("check parent account: %w", err)
			}
			if !exists {
				return ErrInvalidAccount
			}
		}

		account = Account{FirmID: firmID, Code: code, Name: name, Type: accType, ParentID: parentID, Currency: currency, IsActive: true}
		err = tx.QueryRow(ctx, `
			INSERT INTO accounts (firm_id, code, name, type, parent_id, currency)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, created_at
		`, firmID, code, name, string(accType), parentID, currency).Scan(&account.ID, &account.CreatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == postgresUniqueViolation {
				return ErrAccountCodeExists
			}
			return fmt.Errorf("insert account: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, account.ID, accountAuditEntityType, createAccountAuditAction, map[string]any{
			"code": code, "name": name, "type": string(accType),
		})
	})
	if err != nil {
		return Account{}, err
	}
	return account, nil
}

// CreateAccountTx is CreateAccount's already-open-transaction counterpart,
// for callers (internal/portability's configuration import) that need
// account creation as one step inside a larger atomic operation - the
// same "*Tx, no authorization of its own, shared by every entry point"
// pattern internal/workflow.DefineWorkflowTx already establishes.
// Returns ErrAccountCodeExists on a (firm_id, code) collision, the exact
// same sentinel CreateAccount itself returns - the caller decides whether
// that's a hard failure or (as configuration import does) a natural
// "already imported, skip" signal.
//
// ciaudit:ignore-firmid-check: shared account-creation primitive; every
// caller has already run its own authorization check before calling this.
func CreateAccountTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, code, name string, accType AccountType, parentID *uuid.UUID, currency string) (Account, error) {
	code = strings.TrimSpace(code)
	name = strings.TrimSpace(name)
	currency = strings.TrimSpace(currency)
	if currency == "" {
		currency = defaultCurrency
	}
	if code == "" || name == "" || !accType.valid() {
		return Account{}, ErrInvalidAccount
	}

	account := Account{FirmID: firmID, Code: code, Name: name, Type: accType, ParentID: parentID, Currency: currency, IsActive: true}
	err := tx.QueryRow(ctx, `
		INSERT INTO accounts (firm_id, code, name, type, parent_id, currency)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at
	`, firmID, code, name, string(accType), parentID, currency).Scan(&account.ID, &account.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == postgresUniqueViolation {
			return Account{}, ErrAccountCodeExists
		}
		return Account{}, fmt.Errorf("insert account: %w", err)
	}
	return account, nil
}

// AccountUpdate is UpdateAccount's partial-update input - a nil field
// leaves that column unchanged, matching internal/workflow.RuleUpdate's
// convention. Code and type are deliberately not here at all: the design
// brief fixes them as immutable once created (an account's identity and
// its place in the accounting equation shouldn't shift under entries
// already posted against it).
type AccountUpdate struct {
	Name     *string
	IsActive *bool
}

// UpdateAccount changes accountID's name and/or active flag within
// firmID. Owner-gated, same tier as CreateAccount. Code and type are
// immutable - see AccountUpdate's doc comment.
func UpdateAccount(ctx context.Context, pool *pgxpool.Pool, firmID, userID, accountID uuid.UUID, update AccountUpdate) (Account, error) {
	if update.Name != nil && strings.TrimSpace(*update.Name) == "" {
		return Account{}, ErrInvalidAccount
	}

	var account Account
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

		var typeStr string
		err = tx.QueryRow(ctx, `
			UPDATE accounts
			SET name = COALESCE($1, name), is_active = COALESCE($2, is_active)
			WHERE id = $3 AND firm_id = $4
			RETURNING id, firm_id, code, name, type, parent_id, currency, is_active, created_at
		`, update.Name, update.IsActive, accountID, firmID).Scan(
			&account.ID, &account.FirmID, &account.Code, &account.Name, &typeStr,
			&account.ParentID, &account.Currency, &account.IsActive, &account.CreatedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAccountNotFound
		}
		if err != nil {
			return fmt.Errorf("update account: %w", err)
		}
		account.Type = AccountType(typeStr)

		changes := map[string]any{}
		if update.Name != nil {
			changes["name"] = *update.Name
		}
		if update.IsActive != nil {
			changes["isActive"] = *update.IsActive
		}
		return auditlog.Write(ctx, tx, firmID, userID, account.ID, accountAuditEntityType, updateAccountAuditAction, changes)
	})
	if err != nil {
		return Account{}, err
	}
	return account, nil
}

// ListAccounts returns every accounts row for firmID, ordered by code -
// the chart-of-accounts management page's data source. Member-gated (not
// owner-gated): any member can see the chart of accounts, the same
// visibility level as internal/workflow.ListDefinitions - only
// creating/editing an account is owner-only.
func ListAccounts(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Account, error) {
	var accounts []Account
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT id, firm_id, code, name, type, parent_id, currency, is_active, created_at
			FROM accounts
			ORDER BY code
		`)
		if err != nil {
			return fmt.Errorf("list accounts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var a Account
			var typeStr string
			if err := rows.Scan(&a.ID, &a.FirmID, &a.Code, &a.Name, &typeStr, &a.ParentID, &a.Currency, &a.IsActive, &a.CreatedAt); err != nil {
				return err
			}
			a.Type = AccountType(typeStr)
			accounts = append(accounts, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return accounts, nil
}

// GetAccountBalance computes accountID's current balance as SUM(debits) -
// SUM(credits) or SUM(credits) - SUM(debits), whichever direction matches
// the account type's normal balance side (AccountType.normalDebitBalance) -
// the design brief's exact formula. Returned as a decimal string (computed
// by Postgres's exact `numeric` arithmetic and cast to text in-query, see
// the query below) rather than a Go float, so no floating-point rounding
// is ever introduced into a financial figure. Member-gated, same as
// ListAccounts.
func GetAccountBalance(ctx context.Context, pool *pgxpool.Pool, firmID, userID, accountID uuid.UUID) (string, error) {
	var balance string
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var typeStr string
		if err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE id = $1 AND firm_id = $2`, accountID, firmID).Scan(&typeStr); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAccountNotFound
			}
			return fmt.Errorf("look up account: %w", err)
		}

		normalDebit := AccountType(typeStr).normalDebitBalance()
		err = tx.QueryRow(ctx, `
			SELECT (
				CASE WHEN $2 THEN
					COALESCE(SUM(amount) FILTER (WHERE side = 'debit'), 0) - COALESCE(SUM(amount) FILTER (WHERE side = 'credit'), 0)
				ELSE
					COALESCE(SUM(amount) FILTER (WHERE side = 'credit'), 0) - COALESCE(SUM(amount) FILTER (WHERE side = 'debit'), 0)
				END
			)::text
			FROM journal_lines WHERE account_id = $1
		`, accountID, normalDebit).Scan(&balance)
		if err != nil {
			return fmt.Errorf("compute account balance: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return balance, nil
}
