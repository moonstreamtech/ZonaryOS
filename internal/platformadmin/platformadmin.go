// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package platformadmin

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/license"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// viewAction is the audit_log.action value recorded for a platform-admin
// metadata read, kept distinct from auditlog.ViewAction ("view") so a firm
// owner reading their own audit trail can tell "ZonaryOS platform staff
// looked at this firm's metadata" apart from any in-firm view-logging
// (internal/workflow.ListInstances today).
const viewAction = "platform_admin_metadata_view"

// FirmMetadata is the entire shape of what this package exposes about one
// firm - name, creation date, and two counts. Deliberately not extended
// with anything else (Task's explicit data boundary): no join to any
// tenant-business-data table, no user roster, no permission detail.
type FirmMetadata struct {
	FirmID                  uuid.UUID
	Name                    string
	CreatedAt               time.Time
	WorkflowDefinitionCount int
	MemberCount             int
}

// ListFirms is this package's one read: for every firm in the system,
// name/created_at plus a workflow-definition count and a member count.
// caller must be on allow (checked first, before ResolveOrCreateUser or
// any query - see below) or this returns ErrNotPlatformAdmin immediately.
//
// lic is internal/license's gate (Open Points item 32) - this is that
// batch's chosen "at least one real gate" wiring, picked as the lowest-
// blast-radius illustrative fit: this read is already gated by an
// allowlist, already narrow in scope (metadata only, no tenant business
// data - see this package's doc comment), and adding a second, orthogonal
// gate on top of an already-restricted path can't newly expose anything
// that wasn't already behind the allowlist. The license check runs AFTER
// the allowlist check (an unauthorized caller still just gets
// ErrNotPlatformAdmin -> 404, never learns anything about license state)
// but BEFORE any database access, mirroring the allowlist's own
// check-before-any-query placement. lic may be nil (license.Verifier's
// methods are nil-safe and always allow) - callers that don't care about
// license enforcement at all (e.g. this package's own non-license tests)
// can pass nil.
func ListFirms(ctx context.Context, pool *pgxpool.Pool, allow Allowlist, caller identity.Identity, lic *license.Verifier) ([]FirmMetadata, error) {
	// Allowlist check first, before any database access at all (including
	// resolving caller into ZonaryOS's own users table) - caller.Email
	// comes from the already-verified bearer token
	// (internal/identity.Middleware), so this decision needs no query.
	if !allow.Contains(caller.Email) {
		return nil, ErrNotPlatformAdmin
	}

	// License gate, second: still before any database access. When lic
	// is nil or enforcement is disabled (internal/license.Verifier.Allow's
	// own structural guarantee), this is a no-op and returns nil
	// immediately - so with ZONARYOS_LICENSE_ENFORCEMENT unset (the
	// default), this line changes nothing about this function's
	// behavior versus before this batch existed.
	if err := lic.Allow(license.ModulePlatformAdmin); err != nil {
		return nil, err
	}

	callerUserID, err := identity.ResolveOrCreateUser(ctx, pool, caller)
	if err != nil {
		return nil, fmt.Errorf("resolve platform admin user: %w", err)
	}

	firms, err := listFirmsBasic(ctx, pool)
	if err != nil {
		return nil, err
	}

	results := make([]FirmMetadata, 0, len(firms))
	for _, f := range firms {
		workflowCount, memberCount, err := firmCountsAndAuditTx(ctx, pool, f.FirmID, callerUserID)
		if err != nil {
			return nil, fmt.Errorf("read metadata for firm %s: %w", f.FirmID, err)
		}
		f.WorkflowDefinitionCount = workflowCount
		f.MemberCount = memberCount
		results = append(results, f)
	}
	return results, nil
}

// listFirmsBasic reads name/created_at for every firm from the
// un-RLS-scoped `firms` table (see migrations/0001_core_schema.up.sql:
// "Tenant registry. Not RLS-scoped itself"). Explicit column list, no
// SELECT * (Task's explicit data-boundary requirement) - attributes is
// deliberately excluded even though it exists on the row: it is
// free-form, firm-set jsonb, not the fixed metadata surface this package
// promises.
func listFirmsBasic(ctx context.Context, pool *pgxpool.Pool) ([]FirmMetadata, error) {
	rows, err := pool.Query(ctx, `SELECT id, name, created_at FROM firms ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list firms: %w", err)
	}
	defer rows.Close()

	var firms []FirmMetadata
	for rows.Next() {
		var f FirmMetadata
		if err := rows.Scan(&f.FirmID, &f.Name, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan firm row: %w", err)
		}
		firms = append(firms, f)
	}
	return firms, rows.Err()
}

// firmCountsAndAuditTx opens one RLS-scoped transaction for firmID (via
// zdb.WithFirmContext) and, inside it: counts workflow_definitions and
// user_firm_roles (explicit COUNT(*) queries against exactly those two
// tables - nothing else is touched, see this package's doc comment on the
// tenant-data boundary), then writes one audit_log row to firmID's own
// audit trail recording that a platform admin read its metadata, so a
// firm's own owner can see this access the same way they see any other
// audit_log entry (internal/auditlog.List) - no separate/parallel logging
// path, and no invented "platform-level" firm_id: the read genuinely
// happened while scoped to this one firm, so its own audit_log is exactly
// where accountability for it belongs.
//
// ciaudit:ignore-firmid-check: this intentionally does not call
// permission.IsMember/Has for firmID - the caller is platform-admin staff,
// not a member of firmID at all, and membership is not what authorizes
// this read (Allowlist.Contains, checked once in ListFirms before this is
// ever reached, is). This is the sanctioned per-firm-scoped-loop pattern
// ListFirms's doc comment describes: zdb.WithFirmContext still confines
// every query below to firmID's own rows via RLS, so isolation between
// firms is enforced by Postgres exactly as everywhere else in this
// codebase - what's different here is *why* the caller may read this
// firm's rows (platform-admin allowlist, not firm membership), not *how*
// isolation is enforced.
func firmCountsAndAuditTx(ctx context.Context, pool *pgxpool.Pool, firmID, callerUserID uuid.UUID) (workflowCount, memberCount int, err error) {
	err = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM workflow_definitions`).Scan(&workflowCount); err != nil {
			return fmt.Errorf("count workflow_definitions: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM user_firm_roles`).Scan(&memberCount); err != nil {
			return fmt.Errorf("count user_firm_roles: %w", err)
		}
		if err := auditlog.Write(ctx, tx, firmID, callerUserID, firmID, "firm", viewAction, nil); err != nil {
			return fmt.Errorf("write platform admin audit entry: %w", err)
		}
		return nil
	})
	return workflowCount, memberCount, err
}
