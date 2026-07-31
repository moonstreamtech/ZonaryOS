// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package firm

import "errors"

var (
	// ErrFirmNotFound means firmID doesn't exist, or the caller isn't a
	// member of it - same "same error either way" convention every other
	// firm-scoped package in this codebase uses (internal/workflow's
	// ErrDefinitionNotFound, internal/auditlog's ErrFirmNotFound, ...): a
	// non-member can't distinguish a firm that's genuinely missing from
	// one that's real but not theirs.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrPermissionDenied means the caller is a member of firmID but not
	// an owner - UpdateName is owner-gated (internal/permission.IsOwner).
	ErrPermissionDenied = errors.New("caller does not have the required permission")

	// ErrInvalidName means the requested name is empty (after trimming
	// whitespace) - firms.name is NOT NULL with no other constraint, but
	// an empty firm name is never a real firm's name.
	ErrInvalidName = errors.New("firm name must not be empty")

	// ErrMetadataTooLong means one of the optional broader-metadata
	// fields (Open Points item 36: address/tax_id/logo_url) exceeds its
	// sanity length bound (see the maxAddressLen/maxTaxIDLen/maxLogoURLLen
	// constants in firm.go). This is a basic garbage-input guard, not
	// fiscal-format validation - see firm.go's Update doc comment and
	// Open Points item 24, which stays open.
	ErrMetadataTooLong = errors.New("a firm metadata field exceeds its maximum allowed length")
)
