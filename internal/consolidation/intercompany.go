// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package consolidation

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
	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
)

// amountPattern mirrors internal/accounting's own (a plain positive
// decimal, at most 4 fraction digits, matching amount's numeric(19,4)
// column) - redeclared, not imported, the same per-package-constant
// convention this codebase already uses throughout (see
// internal/accounting.postgresUniqueViolation's own doc comment for the
// reasoning).
var amountPattern = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)

const intercompanyTransferSourceType = "intercompany_transfer"

// Fixed chart-of-accounts codes an inter-company transfer posts against,
// auto-created (via ensureAccountTx) in whichever firm doesn't already
// have them - see PostIntercompanyTransfer's own doc comment for the
// accounting treatment these model.
const (
	cashAccountCode                   = "1000"
	cashAccountName                   = "Cash"
	intercompanyReceivableAccountCode = "1900"
	intercompanyReceivableAccountName = "Intercompany Receivable"
	intercompanyPayableAccountCode    = "2900"
	intercompanyPayableAccountName    = "Intercompany Payable"
)

// IntercompanyTransaction is one intercompany_transactions row, as
// returned by PostIntercompanyTransfer.
type IntercompanyTransaction struct {
	ID                 uuid.UUID
	GroupID            uuid.UUID
	FromFirmID         uuid.UUID
	ToFirmID           uuid.UUID
	Amount             string
	Currency           string
	Description        string
	FromJournalEntryID *uuid.UUID
	ToJournalEntryID   *uuid.UUID
	CreatedAt          time.Time
}

// ensureAccountTx looks up firmID's account by code, creating it (via
// accounting.CreateAccountTx, the "shared account-creation primitive"
// that package's own doc comment describes) if it doesn't exist yet -
// PostIntercompanyTransfer can't assume every member firm's chart of
// accounts already has a Cash/Intercompany Receivable/Intercompany
// Payable account, and a platform admin posting a transfer is not a
// member of either firm who could be asked to set one up first.
//
// ciaudit:ignore-firmid-check: internal helper, only called by
// PostIntercompanyTransfer, which has already run its own allowlist
// check (platform-admin, not firm membership) and already scoped tx to
// firmID's own RLS context (via zdb.WithTwoFirmContext's scope
// function) before ever reaching this - same division of responsibility
// internal/accounting.CreateAccountTx's own suppression already
// documents for the identical "shared, already-open-tx primitive"
// shape.
func ensureAccountTx(ctx context.Context, tx pgx.Tx, firmID uuid.UUID, code, name string, accType accounting.AccountType) (accounting.Account, error) {
	var a accounting.Account
	var typeStr string
	err := tx.QueryRow(ctx, `
		SELECT id, firm_id, code, name, type, parent_id, currency, is_active, created_at
		FROM accounts WHERE firm_id = $1 AND code = $2
	`, firmID, code).Scan(&a.ID, &a.FirmID, &a.Code, &a.Name, &typeStr, &a.ParentID, &a.Currency, &a.IsActive, &a.CreatedAt)
	if err == nil {
		a.Type = accounting.AccountType(typeStr)
		return a, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return accounting.Account{}, fmt.Errorf("look up account %q: %w", code, err)
	}
	return accounting.CreateAccountTx(ctx, tx, firmID, code, name, accType, nil, "")
}

// groupMemberFirmIDs returns groupID's member firm ids as a set, and
// whether groupID itself exists at all (an existing group with zero
// members is a legitimate, distinct state from "no such group").
func groupMemberFirmIDs(ctx context.Context, pool *pgxpool.Pool, groupID uuid.UUID) (members map[uuid.UUID]bool, groupExists bool, err error) {
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM firm_groups WHERE id = $1)`, groupID).Scan(&groupExists); err != nil {
		return nil, false, fmt.Errorf("check firm group exists: %w", err)
	}
	if !groupExists {
		return nil, false, nil
	}

	rows, err := pool.Query(ctx, `SELECT member_firm_id FROM firm_group_members WHERE group_id = $1`, groupID)
	if err != nil {
		return nil, false, fmt.Errorf("list firm group members: %w", err)
	}
	defer rows.Close()
	members = make(map[uuid.UUID]bool)
	for rows.Next() {
		var firmID uuid.UUID
		if err := rows.Scan(&firmID); err != nil {
			return nil, false, err
		}
		members[firmID] = true
	}
	return members, true, rows.Err()
}

// PostIntercompanyTransfer records a transfer of amount (in currency)
// from fromFirmID to toFirmID within groupID, posting a matching journal
// entry in EACH firm's own books, atomically - either both entries (and
// the intercompany_transactions row tying them together) land, or none
// do. Platform-admin only, per this batch's own design brief.
//
// Accounting treatment: fromFirmID books DR Intercompany Receivable / CR
// Cash (it gave up cash, gained a claim on the other firm); toFirmID
// books DR Cash / CR Intercompany Payable (it received cash, owes it
// back) - the standard treatment for an intercompany cash advance/loan.
// Both accounts are auto-created per firm on first use (ensureAccountTx)
// under fixed codes (this file's own const block) rather than requiring
// each member firm to have pre-configured them - kept intentionally this
// simple (a single fixed pair of accounts, not a configurable mapping)
// per this batch's own scope boundary ("the foundation for group
// structures, not a full consolidation engine").
//
// The RLS challenge and how it's solved: internal/platform/db's
// WithFirmContext always opens its OWN transaction (one call = one
// commit), so two WithFirmContext calls - one per firm - can never be
// atomic together; a failure in the second would leave the first's
// already-committed entry standing. There is no existing precedent in
// this codebase for one Postgres transaction spanning two firms' RLS
// contexts (every platform-admin-vs-firm-data precedent,
// e.g. internal/platformadmin.ListFirms's own per-firm-scoped loop, is a
// sequence of independent, single-firm, read-only transactions). This
// function instead uses zdb.WithTwoFirmContext (added for exactly this
// call site): one transaction, re-scoped via SET LOCAL
// app.current_firm_id first to fromFirmID (post that entry), then to
// toFirmID (post that entry), then commit once. RLS is still fully
// enforced on every single statement - only one firm's context is ever
// active at a time, so this never grants cross-firm query visibility,
// it just lets both firms' inserts share one atomic commit/rollback.
// See zdb.WithTwoFirmContext's own doc comment for the full reasoning
// and the alternatives considered (a superuser/RLS-bypass role - rejected
// as a significant, unprecedented departure from Never-Violate Rule 3;
// two independent transactions with manual compensating rollback -
// rejected as merely best-effort, not truly atomic).
func PostIntercompanyTransfer(ctx context.Context, pool *pgxpool.Pool, allow platformadmin.Allowlist, caller identity.Identity, groupID, fromFirmID, toFirmID uuid.UUID, amount, currency, description string) (IntercompanyTransaction, error) {
	if !allow.Contains(caller.Email) {
		return IntercompanyTransaction{}, platformadmin.ErrNotPlatformAdmin
	}

	description = strings.TrimSpace(description)
	currency = strings.TrimSpace(currency)
	if fromFirmID == toFirmID || description == "" || currency == "" || !amountPattern.MatchString(amount) {
		return IntercompanyTransaction{}, ErrInvalidTransfer
	}

	members, groupExists, err := groupMemberFirmIDs(ctx, pool, groupID)
	if err != nil {
		return IntercompanyTransaction{}, err
	}
	if !groupExists {
		return IntercompanyTransaction{}, ErrGroupNotFound
	}
	if !members[fromFirmID] || !members[toFirmID] {
		return IntercompanyTransaction{}, ErrFirmNotInGroup
	}

	// The caller (a real platform-admin user, not a member of either
	// firm) still needs a real users.id for journal_entries.created_by -
	// same resolve-once-then-reuse pattern
	// internal/platformadmin.ListFirms's own callerUserID establishes.
	callerUserID, err := identity.ResolveOrCreateUser(ctx, pool, caller)
	if err != nil {
		return IntercompanyTransaction{}, fmt.Errorf("resolve platform admin user: %w", err)
	}

	var txn IntercompanyTransaction
	err = zdb.WithTwoFirmContext(ctx, pool, func(ctx context.Context, tx pgx.Tx, scope func(uuid.UUID) error) error {
		if err := scope(fromFirmID); err != nil {
			return err
		}
		if _, err := ensureAccountTx(ctx, tx, fromFirmID, cashAccountCode, cashAccountName, accounting.AccountTypeAsset); err != nil {
			return fmt.Errorf("ensure from-firm cash account: %w", err)
		}
		if _, err := ensureAccountTx(ctx, tx, fromFirmID, intercompanyReceivableAccountCode, intercompanyReceivableAccountName, accounting.AccountTypeAsset); err != nil {
			return fmt.Errorf("ensure from-firm intercompany receivable account: %w", err)
		}
		fromEntryID, err := accounting.PostJournalEntryTx(ctx, tx, fromFirmID, callerUserID, description, intercompanyTransferSourceType, nil, []accounting.LineInput{
			{AccountCode: intercompanyReceivableAccountCode, Side: accounting.SideDebit, Amount: amount, Currency: currency},
			{AccountCode: cashAccountCode, Side: accounting.SideCredit, Amount: amount, Currency: currency},
		})
		if err != nil {
			return fmt.Errorf("post from-firm journal entry: %w", err)
		}

		if err := scope(toFirmID); err != nil {
			return err
		}
		if _, err := ensureAccountTx(ctx, tx, toFirmID, cashAccountCode, cashAccountName, accounting.AccountTypeAsset); err != nil {
			return fmt.Errorf("ensure to-firm cash account: %w", err)
		}
		if _, err := ensureAccountTx(ctx, tx, toFirmID, intercompanyPayableAccountCode, intercompanyPayableAccountName, accounting.AccountTypeLiability); err != nil {
			return fmt.Errorf("ensure to-firm intercompany payable account: %w", err)
		}
		toEntryID, err := accounting.PostJournalEntryTx(ctx, tx, toFirmID, callerUserID, description, intercompanyTransferSourceType, nil, []accounting.LineInput{
			{AccountCode: cashAccountCode, Side: accounting.SideDebit, Amount: amount, Currency: currency},
			{AccountCode: intercompanyPayableAccountCode, Side: accounting.SideCredit, Amount: amount, Currency: currency},
		})
		if err != nil {
			return fmt.Errorf("post to-firm journal entry: %w", err)
		}

		// intercompany_transactions carries no firm_id of its own and has
		// no RLS policy (this migration's own doc comment) - this INSERT
		// doesn't depend on which firm's context is currently active, but
		// it still runs on this SAME tx/commit as both journal entries
		// above, so the group-level record and both ledger postings are
		// genuinely atomic together, not just sequentially consistent.
		if err := tx.QueryRow(ctx, `
			INSERT INTO intercompany_transactions
				(group_id, from_firm_id, to_firm_id, amount, currency, description, from_journal_entry_id, to_journal_entry_id)
			VALUES ($1, $2, $3, $4::numeric, $5, $6, $7, $8)
			RETURNING id, created_at
		`, groupID, fromFirmID, toFirmID, amount, currency, description, fromEntryID, toEntryID).Scan(&txn.ID, &txn.CreatedAt); err != nil {
			return fmt.Errorf("insert intercompany transaction: %w", err)
		}

		txn.GroupID, txn.FromFirmID, txn.ToFirmID = groupID, fromFirmID, toFirmID
		txn.Amount, txn.Currency, txn.Description = amount, currency, description
		txn.FromJournalEntryID, txn.ToJournalEntryID = &fromEntryID, &toEntryID
		return nil
	})
	if err != nil {
		return IntercompanyTransaction{}, err
	}
	return txn, nil
}
