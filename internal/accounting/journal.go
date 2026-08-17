// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package accounting is Vision §3's financial management core: the
// minimal, generic double-entry ledger every other module (inventory
// valuation, purchasing, payroll, ...) eventually posts to - not a full
// accounting suite. The foundational primitive is the journal entry:
// every financial event is recorded as a set of balanced debit/credit
// lines against a firm's own chart of accounts.
//
// The one invariant that must always hold - SUM(debits) == SUM(credits)
// for any posted entry - is enforced at the application layer
// (PostJournalEntryTx below), inside the same transaction that inserts
// the entry and its lines, not as a database CHECK/trigger: a
// cross-row-and-cross-table sum constraint is impractical to express as a
// plain Postgres CHECK, and a trigger-based equivalent would duplicate
// logic this function already owns as the single write path into
// journal_entries/journal_lines. See migrations/0009_financial_management_core.up.sql's
// own doc comment for the same reasoning from the schema side.
//
// Authorization follows this codebase's usual discipline
// (docs/DEVELOPMENT.md's Authorization checklist): IsMember before any
// read, IsOwner before any account/manual-entry write - see each
// function's own doc comment for exactly which tier it sits at.
package accounting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	fx "github.com/moonstreamtech/ZonaryOS/internal/currency"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/queryfilter"
)

const journalEntryAuditEntityType = "journal_entry"
const postJournalEntryAuditAction = "post"

// manualSourceType is what PostJournalEntry (the owner-gated HTTP path)
// records journal_entries.source_type as - distinct from whatever a
// module posting through PostJournalEntryTx directly names (e.g.
// "workflow_instance", see internal/workflow's bridge in engine.go).
const manualSourceType = "manual"

// amountPattern is a plain decimal string with up to 15 integer digits and
// up to 4 fraction digits - matching journal_lines.amount's
// numeric(19,4) column exactly (19 total digits, 4 reserved for the
// fraction leaves 15 for the integer part). Rejects a leading '+'/'-'
// (amounts are always positive; side carries direction, not sign - see
// the migration's own doc comment), scientific notation, and anything
// else that isn't a plain positive decimal.
var amountPattern = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)

// Side is which leg of a journal line this is.
type Side string

const (
	SideDebit  Side = "debit"
	SideCredit Side = "credit"
)

func (s Side) valid() bool {
	return s == SideDebit || s == SideCredit
}

// LineInput is one requested debit or credit leg of a journal entry,
// addressing its account by code (not id) - the same identifier a firm's
// own chart of accounts is defined by (accounts.code), and the same
// identifier internal/workflow's JournalTemplate/LineTemplate resolve
// against (see engine.go). Currency, left empty, defaults to the
// resolved account's own currency (see PostJournalEntryTx) - Vision's
// scope boundary for this batch: no multi-currency conversion, just a
// stored field.
type LineInput struct {
	AccountCode string
	Side        Side
	// Amount is a decimal string (e.g. "150.00"), not a float - see
	// amountPattern's doc comment for why: a financial amount must never
	// pass through floating-point representation on its way into the
	// database.
	Amount   string
	Currency string
	// CostCenterID (budget management/cost centers/financial planning
	// batch) optionally tags this line with a cost center - nil for
	// every call site that doesn't set it (every journal-posting path
	// predating this batch keeps working unchanged). Not validated
	// against internal/costcenter here - this package stays a leaf
	// dependency (see this file's own import list); the FK constraint on
	// journal_lines.cost_center_id is the actual guarantee, and
	// internal/workflow's own costCenterValidator is what checks a
	// caller-supplied cost center actually belongs to the firm before a
	// bridged transition ever reaches this function - see engine.go's
	// resolveJournalLines.
	CostCenterID *uuid.UUID
}

// JournalLine is one posted line, as read back by ListJournalEntries.
type JournalLine struct {
	AccountID   uuid.UUID
	AccountCode string
	AccountName string
	Side        Side
	Amount      string
	Currency    string
}

// JournalEntry is one posted entry with its lines, as read back by
// ListJournalEntries.
type JournalEntry struct {
	ID          uuid.UUID
	PostedAt    time.Time
	Description string
	SourceType  string
	SourceID    *uuid.UUID
	CreatedBy   uuid.UUID
	Lines       []JournalLine
}

// validateLines checks lines is structurally sound before anything is
// written: at least two lines (a single-legged entry can never balance),
// every side/amount well-formed. It deliberately cannot check the balance
// itself (SUM(debits) == SUM(credits)) - that requires resolving each
// line's amount as Postgres's own exact `numeric` arithmetic would, not a
// second, potentially-diverging Go-side decimal implementation - see
// PostJournalEntryTx's post-insert balance check for where that actually
// happens.
func validateLines(description string, lines []LineInput) error {
	if description == "" {
		return fmt.Errorf("%w: description must not be empty", ErrInvalidJournalEntry)
	}
	if len(lines) < 2 {
		return fmt.Errorf("%w: an entry must have at least two lines", ErrInvalidJournalEntry)
	}
	for _, l := range lines {
		if l.AccountCode == "" {
			return fmt.Errorf("%w: line account code must not be empty", ErrInvalidJournalEntry)
		}
		if !l.Side.valid() {
			return fmt.Errorf("%w: line side must be %q or %q, got %q", ErrInvalidJournalEntry, SideDebit, SideCredit, l.Side)
		}
		if !amountPattern.MatchString(l.Amount) {
			return fmt.Errorf("%w: line amount %q must be a positive decimal with at most 4 fraction digits", ErrInvalidJournalEntry, l.Amount)
		}
	}
	return nil
}

// PostJournalEntryTx is PostJournalEntry's core logic against an
// already-open transaction, scoped to firmID (typically via
// WithFirmContext). This is the one shared entry point every caller that
// can post a journal entry goes through - PostJournalEntry (the
// owner-gated HTTP path, below) and internal/workflow's
// ExecuteTransition (the workflow-to-ledger bridge, engine.go) - so the
// balance invariant is guaranteed regardless of which caller is used, the
// same "one shared Tx-level function, every caller goes through it"
// pattern internal/workflow.DefineWorkflowTx already establishes for
// workflow provisioning.
//
// The caller is responsible for its own authorization decision before
// calling this - PostJournalEntryTx makes none of its own (no
// IsMember/IsOwner check here), the same division of responsibility
// DefineWorkflowTx uses: ExecuteTransition has already checked the
// transition's own permission by the time it reaches here, and
// re-checking a *different* permission (owner) would be wrong for that
// caller entirely.
//
// ciaudit:ignore-firmid-check: shared posting primitive: every HTTP-
// reachable caller (PostJournalEntry, internal/workflow.ExecuteTransition)
// has already run its own authorization check before calling this.
func PostJournalEntryTx(ctx context.Context, tx pgx.Tx, firmID, userID uuid.UUID, description, sourceType string, sourceID *uuid.UUID, lines []LineInput) (uuid.UUID, error) {
	if err := validateLines(description, lines); err != nil {
		return uuid.UUID{}, err
	}

	var entryID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO journal_entries (firm_id, description, source_type, source_id, created_by)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5)
		RETURNING id
	`, firmID, description, sourceType, sourceID, userID).Scan(&entryID); err != nil {
		return uuid.UUID{}, fmt.Errorf("insert journal entry: %w", err)
	}

	lineSummaries := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		var accountID uuid.UUID
		var accountCurrency string
		var isActive bool
		err := tx.QueryRow(ctx, `
			SELECT id, currency, is_active FROM accounts WHERE firm_id = $1 AND code = $2
		`, firmID, l.AccountCode).Scan(&accountID, &accountCurrency, &isActive)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.UUID{}, fmt.Errorf("%w: account code %q", ErrAccountNotFound, l.AccountCode)
		}
		if err != nil {
			return uuid.UUID{}, fmt.Errorf("look up account %q: %w", l.AccountCode, err)
		}
		if !isActive {
			return uuid.UUID{}, fmt.Errorf("%w: account code %q", ErrAccountInactive, l.AccountCode)
		}

		// Multi-currency foundation (this batch): a caller-supplied
		// l.Currency that differs from the resolved account's own
		// currency is auto-converted INTO that account's currency before
		// storage - every journal_lines row then ends up self-consistent
		// (amount denominated in its own stored currency), which is what
		// keeps the balance check below (a currency-blind SUM(amount))
		// correct rather than silently comparing apples to oranges.
		//
		// Safety guarantee: a missing exchange rate NEVER fails this
		// transition. ConvertAmountTx's only failure mode besides
		// fx.ErrRateNotFound is a genuine infrastructure error (the query
		// itself failing) - propagated as a real error, same as any other
		// DB failure elsewhere in this function; ErrRateNotFound
		// specifically is caught here and degrades to storing the
		// original, unconverted amount tagged with the account's own
		// currency (a documented 1:1 fallback, logged so an operator can
		// notice and add the missing rate) rather than blocking a real
		// business operation on exchange-rate data entry.
		currency := l.Currency
		amount := l.Amount
		if currency != "" && currency != accountCurrency {
			converted, convErr := fx.ConvertAmountTx(ctx, tx, l.Amount, currency, accountCurrency, time.Now())
			if convErr != nil {
				if !errors.Is(convErr, fx.ErrRateNotFound) {
					return uuid.UUID{}, fmt.Errorf("convert journal line amount for account %q: %w", l.AccountCode, convErr)
				}
				slog.Warn("journal entry: no exchange rate on file, falling back to 1:1", "from", currency, "to", accountCurrency, "accountCode", l.AccountCode)
			} else {
				amount = converted
			}
		}
		if currency == "" || currency != accountCurrency {
			currency = accountCurrency
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO journal_lines (firm_id, entry_id, account_id, amount, side, currency, cost_center_id)
			VALUES ($1, $2, $3, $4::numeric, $5, $6, $7)
		`, firmID, entryID, accountID, amount, string(l.Side), currency, l.CostCenterID); err != nil {
			return uuid.UUID{}, fmt.Errorf("insert journal line for account %q: %w", l.AccountCode, err)
		}
		lineSummaries = append(lineSummaries, map[string]any{
			"accountCode": l.AccountCode, "side": string(l.Side), "amount": amount, "currency": currency,
		})
	}

	// The balance check: computed by Postgres's own exact `numeric`
	// arithmetic (never a Go float), scoped to exactly the lines just
	// inserted for this entry. A nonzero difference means this entry is
	// not balanced - returning ErrUnbalancedEntry here propagates up
	// through this transaction's caller (WithFirmContext or an
	// already-open caller-owned transaction, either way) and rolls back
	// every insert this call made, so an unbalanced entry never commits.
	var difference string
	if err := tx.QueryRow(ctx, `
		SELECT (
			COALESCE(SUM(amount) FILTER (WHERE side = 'debit'), 0) -
			COALESCE(SUM(amount) FILTER (WHERE side = 'credit'), 0)
		)::text
		FROM journal_lines WHERE entry_id = $1
	`, entryID).Scan(&difference); err != nil {
		return uuid.UUID{}, fmt.Errorf("check journal entry balance: %w", err)
	}
	if !isZeroDecimal(difference) {
		return uuid.UUID{}, fmt.Errorf("%w: debits minus credits = %s", ErrUnbalancedEntry, difference)
	}

	if err := auditlog.Write(ctx, tx, firmID, userID, entryID, journalEntryAuditEntityType, postJournalEntryAuditAction, map[string]any{
		"description": description,
		"sourceType":  sourceType,
		"lines":       lineSummaries,
	}); err != nil {
		return uuid.UUID{}, err
	}

	return entryID, nil
}

// isZeroDecimal reports whether s (a plain decimal string Postgres's own
// `numeric::text` cast produced, e.g. "0", "0.0000", "-0.00") represents
// zero - every character is either a digit, '.', or a leading '-', so a
// string made entirely of '0'/'.'/'-' characters is zero; anything else
// in it means at least one nonzero digit is present.
func isZeroDecimal(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r != '0' && r != '.' && r != '-' {
			return false
		}
	}
	return true
}

// PostJournalEntry is PostJournalEntryTx's owner-gated HTTP-reachable
// entry point for a MANUAL journal entry (POST .../journal-entries) - the
// design brief's "most entries will be auto-posted by the workflow
// bridge, but manual entries must be possible." Owner-gated: posting an
// arbitrary ledger entry by hand is a structural firm-financial decision,
// the same tier as CreateAccount/UpdateAccount.
func PostJournalEntry(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, description string, lines []LineInput) (uuid.UUID, error) {
	var entryID uuid.UUID
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

		id, err := PostJournalEntryTx(ctx, tx, firmID, userID, description, manualSourceType, nil, lines)
		entryID = id
		return err
	})
	if err != nil {
		return uuid.UUID{}, err
	}
	return entryID, nil
}

// ListOptions controls paging for ListJournalEntries - same shape as
// internal/auditlog.ListOptions (Limit/Offset only; a zero value returns
// every entry, unpaged).
type ListOptions struct {
	Limit  int
	Offset int
	// Filters (Part 2 of the search/filtering/enrichment batch) reuses
	// internal/reports' own `{field, op, value}` filter vocabulary
	// (internal/queryfilter.Filter) against journalEntryFilterFields,
	// this package's own closed allow-list.
	Filters []queryfilter.Filter
}

// journalEntryFilterFields is ListJournalEntries' own closed field
// allow-list for opts.Filters - the same columns
// internal/reports.entityRegistry["journal_entries"] already lists,
// redeclared here rather than imported (see internal/queryfilter's own
// doc comment for why: internal/reports already imports internal/accounting
// for its KPI dashboard, so the reverse import would cycle).
var journalEntryFilterFields = map[string]queryfilter.FieldDef{
	"id":          {Column: "id", Kind: queryfilter.KindUUID},
	"description": {Column: "description", Kind: queryfilter.KindString},
	"source_type": {Column: "source_type", Kind: queryfilter.KindString},
	"source_id":   {Column: "source_id", Kind: queryfilter.KindUUID},
	"created_by":  {Column: "created_by", Kind: queryfilter.KindUUID},
	"posted_at":   {Column: "posted_at", Kind: queryfilter.KindTimestamp},
	"created_at":  {Column: "created_at", Kind: queryfilter.KindTimestamp},
}

// ListResult is ListJournalEntries's return shape: the (possibly paged)
// entries themselves, plus Total - the count that would match with no
// Limit/Offset applied, mirroring internal/auditlog.ListResult.
type ListResult struct {
	Entries []JournalEntry
	Total   int
}

// ListJournalEntries returns firmID's journal_entries rows, most recent
// first, each with its lines - the /financials page's "recent entries"
// data source. Member-gated (not owner-gated): reading the ledger is
// ordinary firm data visibility, the same tier as
// internal/workflow.ListInstances; only posting/editing the chart of
// accounts is owner-only.
func ListJournalEntries(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListOptions) (ListResult, error) {
	var result ListResult
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		// opts.Filters reuses internal/queryfilter.BuildClause against
		// journalEntryFilterFields - see ListOptions' own doc comment.
		args := []any{opts.Limit, opts.Offset}
		filterClauses, args, err := queryfilter.BuildClause(journalEntryFilterFields, opts.Filters, args)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidFilter, err)
		}
		query := `
			SELECT id, posted_at, description, COALESCE(source_type, ''), source_id, created_by, COUNT(*) OVER()
			FROM journal_entries`
		if len(filterClauses) > 0 {
			query += " WHERE " + strings.Join(filterClauses, " AND ")
		}
		// COUNT(*) OVER() rides along with the paged rows, same
		// convention as internal/auditlog.List/internal/workflow.ListInstances.
		// $1/$2 (LIMIT/OFFSET) are bound first, ahead of any filter
		// params - filterClauses' own $N placeholders were generated by
		// BuildClause counting from args' length AFTER those two, so this
		// ordering must match exactly.
		query += " ORDER BY posted_at DESC, id DESC LIMIT NULLIF($1, 0) OFFSET $2"

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list journal entries: %w", err)
		}
		var entries []JournalEntry
		entryIndex := make(map[uuid.UUID]int)
		for rows.Next() {
			var e JournalEntry
			var total int
			if err := rows.Scan(&e.ID, &e.PostedAt, &e.Description, &e.SourceType, &e.SourceID, &e.CreatedBy, &total); err != nil {
				rows.Close()
				return err
			}
			entryIndex[e.ID] = len(entries)
			entries = append(entries, e)
			result.Total = total
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()

		if len(entries) == 0 {
			result.Entries = entries
			return nil
		}

		entryIDs := make([]uuid.UUID, 0, len(entries))
		for _, e := range entries {
			entryIDs = append(entryIDs, e.ID)
		}

		lineRows, err := tx.Query(ctx, `
			SELECT jl.entry_id, jl.account_id, a.code, a.name, jl.side, jl.amount::text, jl.currency
			FROM journal_lines jl
			JOIN accounts a ON a.id = jl.account_id
			WHERE jl.entry_id = ANY($1)
			ORDER BY jl.created_at
		`, entryIDs)
		if err != nil {
			return fmt.Errorf("list journal lines: %w", err)
		}
		defer lineRows.Close()
		for lineRows.Next() {
			var entryID uuid.UUID
			var line JournalLine
			var sideStr string
			if err := lineRows.Scan(&entryID, &line.AccountID, &line.AccountCode, &line.AccountName, &sideStr, &line.Amount, &line.Currency); err != nil {
				return err
			}
			line.Side = Side(sideStr)
			idx, ok := entryIndex[entryID]
			if !ok {
				continue
			}
			entries[idx].Lines = append(entries[idx].Lines, line)
		}
		if err := lineRows.Err(); err != nil {
			return err
		}

		result.Entries = entries
		return nil
	})
	if err != nil {
		return ListResult{}, err
	}
	return result, nil
}
