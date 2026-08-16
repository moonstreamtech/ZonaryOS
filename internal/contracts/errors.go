// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package contracts

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - contract/document writes are
	// owner-gated, same tier internal/asset's own mutations use.
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's contracts")

	// ErrContractNotFound means no contract_registry row with the given
	// id is visible in the caller's firm context.
	ErrContractNotFound = errors.New("contract not found")

	// ErrInvalidContract means CreateContract/UpdateContract was given a
	// structurally invalid contract (empty reference number/title/
	// counterparty name, unknown type/status).
	ErrInvalidContract = errors.New("invalid contract")

	// ErrDuplicateReferenceNumber means CreateContract/UpdateContract was
	// given a reference_number already used by another contract in the
	// same firm (contract_registry's own UNIQUE (firm_id, reference_number)
	// constraint).
	ErrDuplicateReferenceNumber = errors.New("contract reference number already in use")

	// ErrDocumentNotFound means no contract_documents row with the given
	// id is visible on the given contract.
	ErrDocumentNotFound = errors.New("contract document not found")

	// ErrInvalidDocument means CreateDocument was given structurally
	// invalid input (empty name, unknown type).
	ErrInvalidDocument = errors.New("invalid contract document")
)
