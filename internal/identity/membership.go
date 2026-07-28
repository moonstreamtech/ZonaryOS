package identity

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// FirmMembership is one firm a user belongs to and the role they hold
// there.
type FirmMembership struct {
	FirmID   uuid.UUID
	FirmName string
	RoleID   uuid.UUID
}

// Memberships lists every firm userID belongs to. It runs under
// WithUserContext (app.current_user_id), not WithFirmContext: at this
// point the caller doesn't yet know which firm to scope to - that's
// exactly the bootstrap problem migrations/0002_user_scoped_discovery.sql
// solves, by letting a user_firm_roles row be read either because it
// matches the current firm context or because it belongs to the current
// user. The query only joins `firms`, which is global/unrestricted; it
// deliberately does not join `roles`, which remains firm-scoped-only (see
// RoleInFirm for reading role details once a firm has been chosen).
func Memberships(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) ([]FirmMembership, error) {
	var memberships []FirmMembership

	err := zdb.WithUserContext(ctx, pool, userID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT ufr.firm_id, f.name, ufr.role_id
			FROM user_firm_roles ufr
			JOIN firms f ON f.id = ufr.firm_id
			WHERE ufr.user_id = $1
			ORDER BY f.name
		`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var m FirmMembership
			if err := rows.Scan(&m.FirmID, &m.FirmName, &m.RoleID); err != nil {
				return err
			}
			memberships = append(memberships, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("list firm memberships: %w", err)
	}

	return memberships, nil
}

// RoleDetail is a user's role within one specific firm.
type RoleDetail struct {
	RoleID   uuid.UUID
	RoleKey  string
	RoleName string
}

// RoleInFirm resolves userID's role within firmID by opening a normal,
// firm-scoped WithFirmContext transaction - proving the full chain from a
// verified token through to an RLS-scoped database session for that firm.
// It returns an error (no rows) if userID has no membership in firmID,
// which RLS enforces regardless of what the caller passes in.
func RoleInFirm(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) (RoleDetail, error) {
	var detail RoleDetail

	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT r.id, r.key, r.name
			FROM user_firm_roles ufr
			JOIN roles r ON r.id = ufr.role_id
			WHERE ufr.user_id = $1 AND ufr.firm_id = $2
		`, userID, firmID).Scan(&detail.RoleID, &detail.RoleKey, &detail.RoleName)
	})
	if err != nil {
		return RoleDetail{}, fmt.Errorf("resolve role in firm: %w", err)
	}

	return detail, nil
}
