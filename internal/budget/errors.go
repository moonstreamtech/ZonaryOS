// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package budget

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - budget writes are owner-gated,
	// same tier internal/asset's own mutations use.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's budgets")

	// ErrBudgetNotFound means no budgets row with the given id is
	// visible in the caller's firm context.
	ErrBudgetNotFound = errors.New("budget not found")

	// ErrInvalidBudget means CreateBudget/UpdateBudget was given
	// structurally invalid input (empty name, unknown period_type/
	// status, period_end before period_start).
	ErrInvalidBudget = errors.New("invalid budget")

	// ErrInvalidBudgetTransition means Approve was called on a budget not
	// in the required status (draft) for that transition.
	ErrInvalidBudgetTransition = errors.New("invalid budget status transition")

	// ErrBudgetLineNotFound means no budget_lines row with the given id
	// is visible on the given budget.
	ErrBudgetLineNotFound = errors.New("budget line not found")

	// ErrInvalidBudgetLine means CreateBudgetLine was given structurally
	// invalid input (a non-positive planned_amount, an account_id that
	// doesn't belong to this firm).
	ErrInvalidBudgetLine = errors.New("invalid budget line")

	// ErrDuplicateBudgetLineAccount means CreateBudgetLine was given an
	// account_id already budgeted on this budget (budget_lines' own
	// UNIQUE (budget_id, account_id) constraint).
	ErrDuplicateBudgetLineAccount = errors.New("account already has a budget line on this budget")
)
