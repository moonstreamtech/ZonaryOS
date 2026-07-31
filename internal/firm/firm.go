// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package firm holds the narrow set of firm-level mutations that don't
// belong to any other module - today, exactly one: renaming a firm.
// Deliberately not a general "firm settings" package: docs/OPEN_POINTS.md
// item 36 (filed alongside this) flags that a broader firm-metadata
// editing story (the `firms.attributes` jsonb column, e.g.) is real,
// undesigned future work, not something to build speculatively here.
package firm

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// renameAuditAction is the audit_log.action UpdateName records - reuses
// audit_log's existing shape (entity_type "firm", same table
// internal/wizard.CreateDefaultFirm's own firm-creation entry uses),
// distinguishing a rename from the "create" action that table already
// carries for the same entity_type.
const renameAuditAction = "update"

// UpdateName renames firmID, owner-gated (internal/permission.IsOwner,
// after IsMember - same order every owner-gated function in this
// codebase checks in, e.g. internal/permission.GrantPermission):
// renaming a firm is a structural firm decision, the same tier as
// defining a new workflow (internal/workflow.DefineWorkflowForFirm) or
// creating the firm itself, not something every member can do.
func UpdateName(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrInvalidName
	}

	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrPermissionDenied
		}

		if _, err := tx.Exec(ctx, `UPDATE firms SET name = $1 WHERE id = $2`, name, firmID); err != nil {
			return err
		}

		return auditlog.Write(ctx, tx, firmID, userID, firmID, "firm", renameAuditAction, map[string]any{"name": name})
	})
}
