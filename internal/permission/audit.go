package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// grantAuditAction and revokeAuditAction are the audit_log.action values
// GrantPermission/RevokePermission record - reusing audit_log's existing
// shape (see internal/workflow.CreateInstance), entity_type
// "role_permission", entity_id the affected role's ID.
const (
	grantAuditAction  = "grant"
	revokeAuditAction = "revoke"
)

// IsOwner reports whether userID holds any owner-flagged role
// (roles.is_owner, migrations/0004_role_owner_flag.up.sql) within firmID.
// Must be called within a transaction already scoped to firmID via
// db.WithFirmContext, same as Has/IsMember.
func IsOwner(ctx context.Context, tx pgx.Tx, firmID, userID uuid.UUID) (bool, error) {
	var owner bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM user_firm_roles ufr
			JOIN roles r ON r.id = ufr.role_id
			WHERE ufr.user_id = $1 AND ufr.firm_id = $2 AND r.is_owner
		)
	`, userID, firmID).Scan(&owner)
	if err != nil {
		return false, fmt.Errorf("check owner status: %w", err)
	}
	return owner, nil
}

// RoleGrant is one non-owner role's current permission grants - the
// "entire current role-permission table" Vision §3's Permission Audit
// Mode fetches when turned on, scoped to firmID and already excluding
// owner-flagged roles (never returned to the frontend to filter itself).
type RoleGrant struct {
	RoleID         uuid.UUID
	RoleKey        string
	RoleName       string
	PermissionKeys []string
}

// FirmPermissionAudit is what GetFirmPermissionAudit returns: the
// caller's own permission keys (across all roles they hold, owner or
// not - so their own Audit Mode badges are accurate even though their
// role may be excluded from Roles below) plus every non-owner role's
// grants, for the management popup's role list.
type FirmPermissionAudit struct {
	MyPermissionKeys []string
	Roles            []RoleGrant
}

// GetFirmPermissionAudit is Permission Audit Mode's one read call. Only
// an owner may call it (ErrNotOwner otherwise) - Vision §3 scopes turning
// on Audit Mode to "an authorized user"; see docs/DEVELOPMENT.md for why
// "authorized" means is_owner here.
func GetFirmPermissionAudit(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) (FirmPermissionAudit, error) {
	var result FirmPermissionAudit

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

		myRows, err := tx.Query(ctx, `
			SELECT DISTINCT rp.permission_key
			FROM user_firm_roles ufr
			JOIN role_permissions rp ON rp.role_id = ufr.role_id
			WHERE ufr.user_id = $1
			ORDER BY rp.permission_key
		`, userID)
		if err != nil {
			return fmt.Errorf("list caller's own permission keys: %w", err)
		}
		result.MyPermissionKeys = []string{}
		for myRows.Next() {
			var key string
			if err := myRows.Scan(&key); err != nil {
				myRows.Close()
				return err
			}
			result.MyPermissionKeys = append(result.MyPermissionKeys, key)
		}
		if err := myRows.Err(); err != nil {
			return err
		}
		myRows.Close()

		roleRows, err := tx.Query(ctx, `
			SELECT r.id, r.key, r.name,
				COALESCE(array_agg(rp.permission_key) FILTER (WHERE rp.permission_key IS NOT NULL), '{}')
			FROM roles r
			LEFT JOIN role_permissions rp ON rp.role_id = r.id
			WHERE NOT r.is_owner
			GROUP BY r.id, r.key, r.name
			ORDER BY r.name
		`)
		if err != nil {
			return fmt.Errorf("list role grants: %w", err)
		}
		defer roleRows.Close()
		for roleRows.Next() {
			var g RoleGrant
			if err := roleRows.Scan(&g.RoleID, &g.RoleKey, &g.RoleName, &g.PermissionKeys); err != nil {
				return err
			}
			result.Roles = append(result.Roles, g)
		}
		return roleRows.Err()
	})
	if err != nil {
		return FirmPermissionAudit{}, err
	}
	return result, nil
}

// checkRoleIsGrantable looks up roleID within firmID and rejects it if it
// doesn't exist or is owner-flagged - the server-side half of "owner
// roles don't appear in these lists" (the frontend never offers one as a
// target, but this is checked again here regardless: never trust the
// client for that).
func checkRoleIsGrantable(ctx context.Context, tx pgx.Tx, roleID uuid.UUID) error {
	var isOwnerRole bool
	err := tx.QueryRow(ctx, `SELECT is_owner FROM roles WHERE id = $1`, roleID).Scan(&isOwnerRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRoleNotFound
	}
	if err != nil {
		return fmt.Errorf("look up role: %w", err)
	}
	if isOwnerRole {
		return ErrOwnerRoleImmutable
	}
	return nil
}

func checkPermissionKeyExists(ctx context.Context, tx pgx.Tx, permissionKey string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM permissions WHERE key = $1)`, permissionKey).Scan(&exists); err != nil {
		return fmt.Errorf("check permission catalog: %w", err)
	}
	if !exists {
		return ErrPermissionKeyNotFound
	}
	return nil
}

func writeGrantAuditLog(ctx context.Context, tx pgx.Tx, firmID, userID, roleID uuid.UUID, action, permissionKey string) error {
	changes, err := json.Marshal(map[string]any{"permissionKey": permissionKey})
	if err != nil {
		return fmt.Errorf("marshal audit changes: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (firm_id, user_id, entity_type, entity_id, action, changes)
		VALUES ($1, $2, 'role_permission', $3, $4, $5)
	`, firmID, userID, roleID, action, changes); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// GrantPermission grants permissionKey to roleID within firmID, gated to
// an owner actor (actorUserID) - the caller is responsible for notifying
// any live-session broadcaster after this returns successfully (kept out
// of this function so the core business logic stays testable without an
// SSE dependency; see internal/permission.Broadcaster and handlers.go).
func GrantPermission(ctx context.Context, pool *pgxpool.Pool, firmID, actorUserID, roleID uuid.UUID, permissionKey string) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := IsMember(ctx, tx, firmID, actorUserID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		owner, err := IsOwner(ctx, tx, firmID, actorUserID)
		if err != nil {
			return err
		}
		if !owner {
			return ErrNotOwner
		}
		if err := checkRoleIsGrantable(ctx, tx, roleID); err != nil {
			return err
		}
		if err := checkPermissionKeyExists(ctx, tx, permissionKey); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO role_permissions (firm_id, role_id, permission_key)
			VALUES ($1, $2, $3)
			ON CONFLICT (role_id, permission_key) DO NOTHING
		`, firmID, roleID, permissionKey); err != nil {
			return fmt.Errorf("grant permission: %w", err)
		}

		return writeGrantAuditLog(ctx, tx, firmID, actorUserID, roleID, grantAuditAction, permissionKey)
	})
}

// RevokePermission is GrantPermission's inverse. Revoking a permission a
// role never held is a no-op, not an error (idempotent, like the grant
// side's ON CONFLICT DO NOTHING).
func RevokePermission(ctx context.Context, pool *pgxpool.Pool, firmID, actorUserID, roleID uuid.UUID, permissionKey string) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := IsMember(ctx, tx, firmID, actorUserID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		owner, err := IsOwner(ctx, tx, firmID, actorUserID)
		if err != nil {
			return err
		}
		if !owner {
			return ErrNotOwner
		}
		if err := checkRoleIsGrantable(ctx, tx, roleID); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			DELETE FROM role_permissions WHERE role_id = $1 AND permission_key = $2
		`, roleID, permissionKey); err != nil {
			return fmt.Errorf("revoke permission: %w", err)
		}

		return writeGrantAuditLog(ctx, tx, firmID, actorUserID, roleID, revokeAuditAction, permissionKey)
	})
}
