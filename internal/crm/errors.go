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
)
