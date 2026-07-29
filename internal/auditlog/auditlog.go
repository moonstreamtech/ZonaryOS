// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package auditlog is Vision §3's Audit Trail Infrastructure: a shared,
// reusable place for any module to record a data-change or a view/read
// event against the `audit_log` table (migrations/0003_workflow_engine.up.sql),
// and the one gated read path back onto it.
//
// This package deliberately does not invent a generic "diffing framework" -
// every mutation call site (internal/workflow.CreateInstance/
// ExecuteTransition, internal/wizard.CreateDefaultFirm,
// internal/permission.GrantPermission/RevokePermission) still writes its own
// explicit audit_log row describing exactly what happened, matching this
// codebase's existing modular-monolith style. Write below exists so *new*
// call sites (and this package's own view-log writer) share one INSERT
// instead of duplicating the statement - internal/permission's existing
// inline writer predates this package and is left as-is (see its own doc
// comment for why it can't import this package without an import cycle).
//
// Access is gated the same way every other permission in this codebase is
// (internal/permission.IsMember + Has, per docs/DEVELOPMENT.md's
// Authorization checklist) rather than a hardcoded "is_owner" check, so a
// firm can extend read access to a future "Auditor Role" (Vision §3) by
// simply granting ReadPermission to it via Permission Audit Mode - no code
// change required. Today, CreateDefaultFirm (internal/wizard) is the only
// place that grants it, and it grants it to the firm's own owner role.
package auditlog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// ReadPermission gates GET .../audit-log (see List/handlers.go). Vision §3:
// audit log access belongs to the firm owner and, when a firm invites one,
// an external "Auditor Role" - the Auditor Role itself isn't designed or
// built yet (no invitation flow exists), so this PR only guarantees that
// *whoever holds this permission key* can read the log, through the same
// role_permissions mechanism every other permission in this system already
// uses. CreateDefaultFirm grants it to the founder's owner role at firm
// creation (RegisterReadPermissionTx below); a firm can grant it to any
// other role later via Permission Audit Mode, which is exactly how a future
// Auditor Role would gain access without touching this package.
const ReadPermission = "audit.log.read"

// ViewAction is the audit_log.action value LogView records - kept distinct
// from any data-change action string (e.g. "create", "grant", "record_sale")
// so a reader of the log can cheaply tell "this row was viewed" apart from
// "this row was changed". See docs/OPEN_POINTS.md item 33: whether
// retaining view/read records at all creates KVKK exposure is an open legal
// question, which is exactly why view logging is being rolled out through
// one representative call site (internal/workflow.ListInstances) rather
// than every read endpoint in the system - see that function's doc comment.
const ViewAction = "view"

// RegisterReadPermissionTx upserts ReadPermission into the global
// permissions catalog and, when granteeRoleID is not uuid.Nil, grants it to
// that role within firmID - the same "self-action auto-grant" shape
// internal/workflow.DefineWorkflowTx's upsertPermission already
// established for workflow-introduced permissions, applied here to the
// audit-log-read permission instead. Idempotent either way (ON CONFLICT DO
// NOTHING on both statements). Must be called within a transaction already
// scoped to firmID (or, during firm creation, one that has just set
// app.current_firm_id itself - see internal/wizard.CreateDefaultFirm).
//
// ciaudit:ignore-firmid-check: permission-catalog provisioning helper,
// only ever called by internal/wizard.CreateDefaultFirm with a firmID it
// just created in the same transaction - never reachable with a caller-
// supplied firmID via an HTTP handler.
func RegisterReadPermissionTx(ctx context.Context, tx pgx.Tx, firmID, granteeRoleID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO permissions (key, description)
		VALUES ($1, $2)
		ON CONFLICT (key) DO NOTHING
	`, ReadPermission, "Read this firm's audit trail (data-change and view/read history)."); err != nil {
		return fmt.Errorf("upsert audit log read permission: %w", err)
	}

	if granteeRoleID == uuid.Nil {
		return nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO role_permissions (firm_id, role_id, permission_key)
		VALUES ($1, $2, $3)
		ON CONFLICT (role_id, permission_key) DO NOTHING
	`, firmID, granteeRoleID, ReadPermission); err != nil {
		return fmt.Errorf("grant audit log read permission to role %s: %w", granteeRoleID, err)
	}
	return nil
}

// Write records one audit_log row: who (userID) did what (action) to which
// entity (entityType/entityID) within firmID, and whatever changes describes
// (old/new values, or any other detail worth recording "at the most
// detailed level" - Vision §3) - marshaled to jsonb as-is. changes may be
// nil (recorded as an empty object, same as the column's own default).
// Must be called within a transaction already scoped to firmID via
// zdb.WithFirmContext (or, during firm creation, one that has just set
// app.current_firm_id itself), same requirement as every other write in
// this codebase.
//
// ciaudit:ignore-firmid-check: pure audit_log writer, invoked only after
// its caller has already checked membership/permission for the operation
// it's recording - it makes no read/write authorization decision of its
// own.
func Write(ctx context.Context, tx pgx.Tx, firmID, userID, entityID uuid.UUID, entityType, action string, changes map[string]any) error {
	if changes == nil {
		changes = map[string]any{}
	}
	changesJSON, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("marshal audit changes: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (firm_id, user_id, entity_type, entity_id, action, changes)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, firmID, userID, entityType, entityID, action, changesJSON); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// LogView is Write specialized to ViewAction with no changes payload - the
// reusable call-site pattern for view/read logging Vision §3 calls for.
// Deliberately not wired into every read handler yet: see
// internal/workflow.ListInstances, the one representative read path this PR
// wires it into, and docs/OPEN_POINTS.md item 33 for why broader rollout is
// pending a legal-review decision, not an oversight.
//
// ciaudit:ignore-firmid-check: thin wrapper around Write, same reasoning -
// invoked only after its caller has already checked membership/permission.
func LogView(ctx context.Context, tx pgx.Tx, firmID, userID, entityID uuid.UUID, entityType string) error {
	return Write(ctx, tx, firmID, userID, entityID, entityType, ViewAction, nil)
}

// Entry is one audit_log row as returned by List, with the acting user's
// email/display name joined in (from the global `users` table) so a reader
// of the log doesn't need a separate lookup per row.
type Entry struct {
	ID              uuid.UUID
	EntityType      string
	EntityID        uuid.UUID
	Action          string
	Changes         map[string]any
	UserID          uuid.UUID
	UserEmail       string
	UserDisplayName string
	OccurredAt      time.Time
}

// List returns every audit_log row for firmID, most recent first, gated by
// ReadPermission - not IsOwner - so this naturally extends to a future
// Auditor Role (see ReadPermission's doc comment). IsMember is still
// checked first, same discipline as every other firm-scoped read in this
// codebase (docs/DEVELOPMENT.md's Authorization checklist): WithFirmContext's
// RLS scoping alone only confines the query to rows whose firm_id matches
// whatever firmID the caller passed in, it does not prove the caller
// belongs to that firm at all.
func List(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Entry, error) {
	var results []Entry

	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		allowed, err := permission.Has(ctx, tx, firmID, userID, ReadPermission)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrPermissionDenied
		}

		rows, err := tx.Query(ctx, `
			SELECT al.id, al.entity_type, al.entity_id, al.action, al.changes,
				al.user_id, u.email, u.display_name, al.occurred_at
			FROM audit_log al
			JOIN users u ON u.id = al.user_id
			WHERE al.firm_id = $1
			ORDER BY al.occurred_at DESC
		`, firmID)
		if err != nil {
			return fmt.Errorf("list audit log: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var e Entry
			var changesJSON []byte
			if err := rows.Scan(
				&e.ID, &e.EntityType, &e.EntityID, &e.Action, &changesJSON,
				&e.UserID, &e.UserEmail, &e.UserDisplayName, &e.OccurredAt,
			); err != nil {
				return err
			}
			if err := json.Unmarshal(changesJSON, &e.Changes); err != nil {
				return fmt.Errorf("unmarshal audit changes: %w", err)
			}
			results = append(results, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}
