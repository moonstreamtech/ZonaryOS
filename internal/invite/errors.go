// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package invite

import "errors"

var (
	// ErrFirmNotFound means firmID doesn't exist, or the caller isn't a
	// member of it - same "same error either way" convention every other
	// firm-scoped package in this codebase uses (internal/firm.ErrFirmNotFound,
	// internal/auditlog.ErrFirmNotFound, ...).
	ErrFirmNotFound = errors.New("firm not found")

	// ErrPermissionDenied means the caller is a member of firmID but not
	// an owner - Generate/List/Revoke are all owner-gated, same
	// IsMember-then-IsOwner order as internal/firm.Update.
	ErrPermissionDenied = errors.New("caller does not have the required permission")

	// ErrRoleNotFound means the given roleID does not resolve to a role
	// within firmID. This is deliberately the *same* error whether the
	// role simply doesn't exist or exists but belongs to a different
	// firm: Generate looks it up inside a transaction already scoped to
	// firmID (db.WithFirmContext), so roles.roles_tenant_isolation RLS
	// policy (migrations/0001_core_schema.up.sql) makes a cross-firm
	// roleID structurally invisible to that query - there is no
	// application-level "does roleID belong to firmID" branch to get
	// wrong, the database itself never returns the row.
	ErrRoleNotFound = errors.New("role not found in this firm")

	// ErrInviteNotFound means no firm_invites row exists for the given
	// token at all - distinct from ErrInviteExpired/Used/Revoked (an
	// invite that once existed and is simply no longer acceptable), so
	// the accept page can show a genuinely different message for "this
	// link is wrong" versus "this link was valid once."
	ErrInviteNotFound = errors.New("invite not found")

	// ErrInviteExpired means the invite's expires_at has passed and it
	// was never used or revoked - checked at Lookup/Accept time against
	// the persisted status, not a background job that flips the status
	// column: an expired invite still shows in Owner-facing List as
	// "pending" (its stored status truly is still 'pending'), with the
	// UI computing "expired" the same way Lookup/Accept do, from
	// expires_at alone.
	ErrInviteExpired = errors.New("invite has expired")

	// ErrInviteUsed means this invite's single use has already been
	// consumed - a second Accept attempt on the same token must fail
	// cleanly with this, not silently re-grant the role or silently
	// succeed a no-op.
	ErrInviteUsed = errors.New("invite has already been used")

	// ErrInviteRevoked means the firm owner revoked this invite before it
	// was accepted (Revoke) - a revoked invite can never move to 'used'.
	ErrInviteRevoked = errors.New("invite has been revoked")

	// ErrInviteNotPending means Revoke was called on an invite that is
	// already used or revoked - only a 'pending' invite can be revoked.
	ErrInviteNotPending = errors.New("invite is not pending")
)
