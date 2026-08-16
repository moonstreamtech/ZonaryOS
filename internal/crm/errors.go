// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package crm

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - customer writes are owner-gated
	// (see this package's doc comment).
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's customers")

	// ErrCustomerNotFound means no customers row with the given id is
	// visible in the caller's firm context.
	ErrCustomerNotFound = errors.New("customer not found")

	// ErrInvalidCustomer means CreateCustomer/UpdateCustomer/CreateCustomerTx
	// was given a structurally invalid customer (empty name, malformed
	// credit limit).
	ErrInvalidCustomer = errors.New("invalid customer")

	// ErrInteractionNotFound means no crm_interactions row with the given
	// id is visible in the caller's firm context.
	ErrInteractionNotFound = errors.New("interaction not found")

	// ErrInvalidInteraction means CreateInteraction was given a
	// structurally invalid interaction (empty summary, unknown type).
	ErrInvalidInteraction = errors.New("invalid interaction")

	// ErrOpportunityNotFound means no crm_opportunities row with the
	// given id is visible in the caller's firm context.
	ErrOpportunityNotFound = errors.New("opportunity not found")

	// ErrInvalidOpportunity means CreateOpportunity/UpdateOpportunity was
	// given a structurally invalid opportunity (empty name, malformed
	// value, out-of-range probability, unknown stage).
	ErrInvalidOpportunity = errors.New("invalid opportunity")

	// ErrInvalidOpportunityTransition means UpdateOpportunityStage was
	// called on an opportunity already in a terminal stage (won/lost) -
	// a closed deal doesn't reopen.
	ErrInvalidOpportunityTransition = errors.New("invalid opportunity stage transition")
)
