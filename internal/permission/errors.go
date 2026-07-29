// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package permission

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm at
	// all - deliberately the same shape as "the firm doesn't exist" (see
	// internal/workflow's writeEngineError convention): a non-member
	// should never be able to distinguish a real firm they don't belong
	// to from one that doesn't exist.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - Permission Audit Mode's management
	// actions (list/grant/revoke) are restricted to this (see
	// docs/DEVELOPMENT.md's note on this working assumption).
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's permissions")

	// ErrRoleNotFound means roleID doesn't exist in the caller's firm.
	ErrRoleNotFound = errors.New("role not found in this firm")

	// ErrOwnerRoleImmutable means roleID is flagged is_owner
	// (migrations/0004_role_owner_flag.up.sql) - Vision §3: "roles at the
	// highest permission tier ... don't appear in these lists." Grant/
	// revoke reject these server-side, never trusting the frontend to
	// have filtered them out of its own UI.
	ErrOwnerRoleImmutable = errors.New("owner-flagged roles cannot be granted or revoked permissions through this endpoint")

	// ErrPermissionKeyNotFound means the given key isn't in the global
	// permissions catalog - role_permissions.permission_key has a foreign
	// key against it, so an unknown key would fail there anyway; this is
	// checked first for a clean error instead of a raw constraint
	// violation.
	ErrPermissionKeyNotFound = errors.New("permission key not found in the global catalog")
)
