// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package accounting

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm at
	// all - same "non-member and nonexistent look identical" convention as
	// internal/permission.ErrFirmNotFound and internal/workflow's
	// ErrDefinitionNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - account creation/update and manual
	// journal entries are owner-gated (see this package's doc comment).
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's chart of accounts or ledger")

	// ErrAccountNotFound means no accounts row with the given id/code is
	// visible in the caller's firm context.
	ErrAccountNotFound = errors.New("account not found")

	// ErrAccountCodeExists means firmID already has an accounts row with
	// the requested code (UNIQUE (firm_id, code), see the financial-core
	// migration).
	ErrAccountCodeExists = errors.New("an account with this code already exists for this firm")

	// ErrInvalidAccount means CreateAccount/UpdateAccount was given a
	// structurally invalid account (empty code/name, unknown type, a
	// parent that doesn't resolve within the same firm).
	ErrInvalidAccount = errors.New("invalid account")

	// ErrAccountInactive means a journal line named an account that
	// exists but has been deactivated - PostJournalEntry refuses to post
	// against it rather than silently reviving it.
	ErrAccountInactive = errors.New("cannot post a journal line against an inactive account")

	// ErrInvalidJournalEntry means PostJournalEntry was given a
	// structurally invalid entry: fewer than two lines, an empty
	// description, an unknown side, or a malformed amount.
	ErrInvalidJournalEntry = errors.New("invalid journal entry")

	// ErrUnbalancedEntry means a journal entry's debit lines and credit
	// lines don't sum to the same total - the one non-negotiable
	// double-entry invariant (see this package's doc comment). Enforced
	// in PostJournalEntryTx, application-side, inside the same
	// transaction as the entry's insert, so an unbalanced entry never
	// commits.
	ErrUnbalancedEntry = errors.New("journal entry is not balanced: total debits must equal total credits")

	// ErrInvalidFilter means ListOptions.Filters referenced a field/op
	// internal/queryfilter.BuildClause rejected against
	// journalEntryFilterFields - see ListJournalEntries' own doc comment.
	ErrInvalidFilter = errors.New("invalid filter")
)
