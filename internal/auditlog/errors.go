package auditlog

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm at
	// all - same fail-closed convention as internal/permission's and
	// internal/workflow's sentinel of the same name: a non-member should
	// never be able to distinguish a real firm they don't belong to from
	// one that doesn't exist.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrPermissionDenied means the caller is a real member of the firm but
	// does not hold ReadPermission through any role assigned to them there
	// - Vision §3 scopes audit log access to the firm owner and, later, an
	// "Auditor Role"; see ReadPermission's doc comment.
	ErrPermissionDenied = errors.New("caller does not have the required permission")
)
