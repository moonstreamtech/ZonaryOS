// Package permission checks a user's permission grants within a firm,
// against the global permission catalog and per-firm role grants
// established in migrations/0001_core_schema.up.sql (`permissions`,
// `roles`, `role_permissions`, `user_firm_roles`). This is the Never-
// Violate Rule 7 "permission tag" check - any module gating an action
// behind a permission key (the workflow engine is the first) should use
// this, not a bespoke check of its own.
package permission

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Has reports whether userID holds permissionKey in firmID, through any of
// the roles they've been assigned there (a user can hold more than one
// role per firm - see user_firm_roles's unique constraint). Must be called
// within a transaction already scoped to firmID via db.WithFirmContext;
// firmID is passed explicitly here only so the query is unambiguous even
// though RLS also confines it.
func Has(ctx context.Context, tx pgx.Tx, firmID, userID uuid.UUID, permissionKey string) (bool, error) {
	var has bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM user_firm_roles ufr
			JOIN role_permissions rp ON rp.role_id = ufr.role_id
			WHERE ufr.user_id = $1 AND ufr.firm_id = $2 AND rp.permission_key = $3
		)
	`, userID, firmID, permissionKey).Scan(&has)
	if err != nil {
		return false, fmt.Errorf("check permission %q: %w", permissionKey, err)
	}
	return has, nil
}
