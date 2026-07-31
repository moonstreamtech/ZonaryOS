// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// postgresUniqueViolation is the SQLSTATE code Postgres raises for a
// UNIQUE constraint violation - internal/workflow.postgresUniqueViolation
// is the same constant under a different package, unexported there too,
// so it's redeclared here rather than imported (this package already sits
// below internal/workflow in the dependency graph - internal/workflow
// imports internal/permission, not the other way around - so importing
// it here would cycle).
const postgresUniqueViolation = "23505"

// roleAuditEntityType is the audit_log.entity_type role create/rename/
// delete writes under - a new value alongside this table's existing
// "firm", "workflow_instance", "workflow_instance_list", and
// "role_permission" entity types (see internal/auditlog and this
// package's own writeGrantAuditLog).
const roleAuditEntityType = "role"

const (
	createRoleAuditAction = "create"
	renameRoleAuditAction = "rename"
	deleteRoleAuditAction = "delete"
)

// CreateRole adds a new, non-owner role to firmID - the missing half of
// Vision §3's Parametric Permission/Role System that
// internal/wizard.CreateDefaultFirm's own doc comment already flags:
// "the firm owner [is expected to] configure roles for everyone else ...
// via Permission Audit Mode," but until this function existed there was
// no way to create a role at all - every firm was permanently stuck with
// exactly the one is_owner role the wizard seeds. Owner-gated, same
// IsMember-then-IsOwner order as GrantPermission/RevokePermission. The
// new role starts with zero permission grants (is_owner is always
// false here - only CreateDefaultFirm ever sets it true, at firm
// creation) - GetFirmPermissionAudit/GetFirmPermissionCatalog already
// list every non-owner role via a LEFT JOIN against role_permissions, so
// a role with no grants yet shows up automatically, nothing new needed
// on that read path.
func CreateRole(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, key, name string) (uuid.UUID, error) {
	key = strings.TrimSpace(key)
	name = strings.TrimSpace(name)
	if key == "" {
		return uuid.UUID{}, ErrInvalidRoleKey
	}
	if name == "" {
		return uuid.UUID{}, ErrInvalidRoleName
	}

	var roleID uuid.UUID
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		owner, err := IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !owner {
			return ErrNotOwner
		}

		err = tx.QueryRow(ctx, `
			INSERT INTO roles (firm_id, key, name, is_owner) VALUES ($1, $2, $3, false) RETURNING id
		`, firmID, key, name).Scan(&roleID)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == postgresUniqueViolation {
				return ErrRoleKeyExists
			}
			return fmt.Errorf("insert role: %w", err)
		}

		return writeRoleAuditLog(ctx, tx, firmID, userID, roleID, createRoleAuditAction, map[string]any{"key": key, "name": name})
	})
	if err != nil {
		return uuid.UUID{}, err
	}
	return roleID, nil
}

// RenameRole changes roleID's display name within firmID. Owner-gated;
// rejects an owner-flagged target the same way GrantPermission/
// RevokePermission do (checkRoleIsGrantable, ErrOwnerRoleImmutable) -
// the is_owner role's name ("Owner") is the one structural anchor
// Permission Audit Mode's own "roles at the highest permission tier ...
// don't appear in these lists" exclusion depends on staying recognizable;
// nothing about that exclusion is name-based today, but there's no
// legitimate reason to let this surface rename it either. roleID's key
// (the machine-readable identifier role_permissions/user_firm_roles
// reference) is deliberately not editable here - unlike a display name,
// changing it would be a breaking identity change for anything that
// already references the role by key, and nothing in this codebase
// currently needs that.
func RenameRole(ctx context.Context, pool *pgxpool.Pool, firmID, userID, roleID uuid.UUID, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return ErrInvalidRoleName
	}

	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		owner, err := IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !owner {
			return ErrNotOwner
		}
		if err := checkRoleIsGrantable(ctx, tx, roleID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `UPDATE roles SET name = $1 WHERE id = $2`, newName, roleID); err != nil {
			return fmt.Errorf("rename role: %w", err)
		}

		return writeRoleAuditLog(ctx, tx, firmID, userID, roleID, renameRoleAuditAction, map[string]any{"name": newName})
	})
}

// DeleteRole removes roleID from firmID. Owner-gated, same
// checkRoleIsGrantable guard as RenameRole (an owner-flagged role can
// never be deleted through this surface - see CreateDefaultFirm and
// docs/DEVELOPMENT.md's Permission Audit Mode section on why the
// is_owner role is the one structural anchor the whole permission system
// depends on).
//
// Deleting a role still holding members is blocked (ErrRoleHasMembers,
// mapped to 409 - the same "real 4xx, not a silent side effect" shape
// internal/workflow.ErrDefinitionKeyExists uses for its own conflict
// case), not cascade-unassigned: user_firm_roles.role_id has no
// ON DELETE SET NULL/CASCADE-to-a-default-role path in this schema (see
// migrations/0001_core_schema.up.sql - it's ON DELETE CASCADE on the
// role, meaning a cascading delete would silently remove those members'
// firm membership entirely, not just their role). Silently kicking
// members out of a firm as a side effect of a role rename/cleanup is a
// far more surprising and harder-to-reverse outcome than a blocked
// delete with a clear reason - the safer default until there's an actual
// product decision about role reassignment on delete (there is no UI or
// backend concept yet for "move these members to another role first").
func DeleteRole(ctx context.Context, pool *pgxpool.Pool, firmID, userID, roleID uuid.UUID) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		owner, err := IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !owner {
			return ErrNotOwner
		}
		if err := checkRoleIsGrantable(ctx, tx, roleID); err != nil {
			return err
		}

		var memberCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM user_firm_roles WHERE role_id = $1`, roleID).Scan(&memberCount); err != nil {
			return fmt.Errorf("count role members: %w", err)
		}
		if memberCount > 0 {
			return ErrRoleHasMembers
		}

		// role_permissions.role_id is ON DELETE CASCADE (migrations/0001_
		// core_schema.up.sql), so this role's own grants are cleaned up
		// automatically - nothing left over for GetFirmPermissionAudit/
		// GetFirmPermissionCatalog to trip over.
		if _, err := tx.Exec(ctx, `DELETE FROM roles WHERE id = $1`, roleID); err != nil {
			return fmt.Errorf("delete role: %w", err)
		}

		return writeRoleAuditLog(ctx, tx, firmID, userID, roleID, deleteRoleAuditAction, map[string]any{})
	})
}

// FirmMember is one row of firmID's roster - a user_firm_roles row
// joined against users and roles for display. A user can hold more than
// one role in the same firm (user_firm_roles' UNIQUE constraint is
// (user_id, firm_id, role_id), not (user_id, firm_id) - see
// migrations/0001_core_schema.up.sql), so ListMembers returns one
// FirmMember per (user, role) pairing rather than trying to collapse a
// user with multiple roles into a single row.
type FirmMember struct {
	UserID      uuid.UUID
	Email       string
	DisplayName string
	RoleID      uuid.UUID
	RoleKey     string
	RoleName    string
}

// ListMembers lists every (user, role) pairing in firmID - item 3's firm
// roster: who's in the firm and which role they hold. Membership-gated
// only (IsMember, not IsOwner) - unlike Permission Audit Mode's grant
// data, "who's on my team and what's their role" isn't treated as
// owner-only sensitive information in this codebase; any member can see
// their own firm's roster, the same visibility level as the generic
// workflow instance list (internal/workflow.ListInstances). A second real
// user now has a path into a firm without any email infrastructure - see
// internal/invite (docs/DEVELOPMENT.md's "Invites" section) - so this can
// return more than the one row for whoever ran the firm-creation wizard;
// Open Points item 17 (email integration) itself stays fully deferred
// either way.
func ListMembers(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]FirmMember, error) {
	var members []FirmMember
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT u.id, u.email, u.display_name, r.id, r.key, r.name
			FROM user_firm_roles ufr
			JOIN users u ON u.id = ufr.user_id
			JOIN roles r ON r.id = ufr.role_id
			WHERE ufr.firm_id = $1
			ORDER BY u.email, r.name
		`, firmID)
		if err != nil {
			return fmt.Errorf("list firm members: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var m FirmMember
			if err := rows.Scan(&m.UserID, &m.Email, &m.DisplayName, &m.RoleID, &m.RoleKey, &m.RoleName); err != nil {
				return err
			}
			members = append(members, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return members, nil
}

// writeRoleAuditLog is writeGrantAuditLog's counterpart for role create/
// rename/delete, called only after CreateRole/RenameRole/DeleteRole have
// already checked IsMember+IsOwner.
//
// ciaudit:ignore-firmid-check: authorization already checked by the
// caller before this runs; see doc comment above.
func writeRoleAuditLog(ctx context.Context, tx pgx.Tx, firmID, userID, roleID uuid.UUID, action string, changes map[string]any) error {
	changesJSON, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("marshal audit changes: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (firm_id, user_id, entity_type, entity_id, action, changes)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, firmID, userID, roleAuditEntityType, roleID, action, changesJSON); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}
