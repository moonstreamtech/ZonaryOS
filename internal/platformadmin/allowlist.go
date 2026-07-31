// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package platformadmin is a narrow, read-only view for ZonaryOS-the-
// company staff (the platform operator), distinct from anything a firm
// member can see: firm name/created_at, workflow-definition counts, and
// member counts, across every firm - never tenant business data (no stock
// items, no CRM leads, no audit log contents). Kept as its own package
// rather than folded into internal/permission or internal/firm precisely
// because it answers a different question ("is this caller ZonaryOS
// staff?") than everything else in those packages ("is this caller a
// member/holder of a permission/owner *within one firm*?") - conflating
// the two would make both harder to audit.
//
// Auth model (Task 1's investigated decision): a hardcoded, env-var-driven
// allowlist of emails, checked against the caller's already-verified
// Keycloak identity (internal/identity.Identity.Email, the same struct
// every other handler in this codebase reads from context after
// internal/identity.Middleware runs) - not a second identity provider or a
// new Keycloak realm/client. Reasoning: a platform operator's staff list
// is small, changes rarely, and doesn't need self-service invitation,
// session management, or its own role model the way a firm's membership
// does - everything a dedicated Keycloak client would buy over a plain
// allowlist. A separate realm/client would add real infrastructure (a new
// client registration, its own token issuance/rotation, a second
// Middleware-like verifier) to solve a problem this batch doesn't have:
// nobody has asked for platform-admin self-service signup, delegation, or
// per-admin scoping yet. If any of those become real requirements, that's
// the concrete reason to revisit this decision - not a chosen default.
//
// Every read this package allows is logged (see ListFirms) to
// internal/auditlog, the same accountability discipline this codebase
// already applies to firm-scoped reads/writes.
package platformadmin

import "strings"

// Allowlist is the set of emails permitted to use this package's read
// path. Constructed once at startup (see cmd/server/main.go) from
// internal/platform/config.Config.PlatformAdminEmails -
// ZONARYOS_PLATFORM_ADMIN_EMAILS, a comma-separated env var following this
// codebase's existing internal/platform/config convention rather than a
// bespoke config mechanism. An empty Allowlist denies everyone - there is
// no implicit platform admin.
type Allowlist struct {
	emails map[string]struct{}
}

// NewAllowlist builds an Allowlist from a slice of emails, normalizing
// each (trim, lower-case) so comparison in Contains is case/whitespace
// insensitive - email casing is not meaningful identity data and callers
// (env vars, humans editing them) shouldn't have to get it exactly right.
func NewAllowlist(emails []string) Allowlist {
	set := make(map[string]struct{}, len(emails))
	for _, e := range emails {
		e = normalizeEmail(e)
		if e == "" {
			continue
		}
		set[e] = struct{}{}
	}
	return Allowlist{emails: set}
}

// Contains reports whether email is on the allowlist. This is the entire
// authorization decision for this package - see ListFirms for where it's
// required to run before any database access.
func (a Allowlist) Contains(email string) bool {
	_, ok := a.emails[normalizeEmail(email)]
	return ok
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
