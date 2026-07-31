// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package invite is the second-user-onboarding mechanism Open Points item
// 17 (email integration) explicitly does not provide: an owner generates a
// link carrying an opaque, unguessable token bound to one firm and one
// role, copies it out of the UI, and hands it to whoever they want to
// invite by whatever channel they already use. Nothing in this package
// sends anything anywhere - see docs/DEVELOPMENT.md's "Invites" section for
// the self-registration investigation this package's accept flow builds
// on.
package invite

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// DefaultExpiry is how long a freshly generated invite stays acceptable
// (the brief's "7 days default") - not configurable per-invite yet, since
// nothing in this batch's scope asked for a per-invite override.
const DefaultExpiry = 7 * 24 * time.Hour

// inviteAuditEntityType is the audit_log.entity_type this package's
// create/revoke/accept actions are recorded under - a new value alongside
// internal/permission's "role"/"role_permission" and internal/firm's
// "firm" (see internal/auditlog's package doc comment for the shared
// audit_log table this reuses, not a bespoke one).
const inviteAuditEntityType = "firm_invite"

const (
	createInviteAuditAction = "create"
	revokeInviteAuditAction = "revoke"
	acceptInviteAuditAction = "accept"
)

// Status values stored in firm_invites.status
// (migrations/0007_firm_invites.up.sql). "expired" is deliberately NOT one
// of them - see ErrInviteExpired's doc comment: expiry is derived from
// expires_at at read time, never persisted as its own status.
const (
	StatusPending = "pending"
	StatusUsed    = "used"
	StatusRevoked = "revoked"
)

// tokenBytes is how much randomness backs each invite token - the same
// 32-byte width web/src/lib/pkce.ts's generateCodeVerifier already uses
// for this codebase's other unguessable-token case (the PKCE verifier),
// picked here for the same reason: 256 bits of crypto/rand output is far
// beyond brute-forceable, and matching an existing, already-reviewed
// choice is preferable to picking a new number. There is no equivalent
// Go-side helper to import - PKCE's verifier/state generation lives in
// the Next.js frontend (TypeScript's crypto.randomBytes), not the Go
// backend - so this is the direct Go-stdlib analog of that same
// crypto.randomBytes(32).toString("base64url") shape:
// crypto/rand.Read plus base64.RawURLEncoding.
const tokenBytes = 32

// generateToken returns a fresh, unguessable, URL-safe invite token.
func generateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate invite token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Invite is one firm_invites row, as Generate/List return it.
type Invite struct {
	ID              uuid.UUID
	FirmID          uuid.UUID
	RoleID          uuid.UUID
	RoleName        string
	Token           string
	Status          string
	Expired         bool
	ExpiresAt       time.Time
	CreatedByUserID uuid.UUID
	CreatedAt       time.Time
	UsedAt          *time.Time
	UsedByUserID    *uuid.UUID
}

// Generate creates a new pending invite for firmID, granting roleID on
// accept, expiring DefaultExpiry from now. Owner-gated, same
// IsMember-then-IsOwner order as internal/firm.Update/internal/permission.CreateRole.
// roleID is validated by simply reading it back inside this same
// firm-scoped transaction - see ErrRoleNotFound's doc comment for why that
// alone is sufficient to reject a role ID from a different firm, with no
// separate "does this role belong to this firm" check needed.
func Generate(ctx context.Context, pool *pgxpool.Pool, firmID, userID, roleID uuid.UUID) (Invite, error) {
	var result Invite
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		owner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !owner {
			return ErrPermissionDenied
		}

		var roleName string
		if err := tx.QueryRow(ctx, `SELECT name FROM roles WHERE id = $1`, roleID).Scan(&roleName); err != nil {
			if err == pgx.ErrNoRows {
				return ErrRoleNotFound
			}
			return fmt.Errorf("look up role: %w", err)
		}

		token, err := generateToken()
		if err != nil {
			return err
		}

		expiresAt := time.Now().Add(DefaultExpiry)
		result = Invite{
			FirmID:          firmID,
			RoleID:          roleID,
			RoleName:        roleName,
			Token:           token,
			Status:          StatusPending,
			ExpiresAt:       expiresAt,
			CreatedByUserID: userID,
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO firm_invites (firm_id, role_id, token, status, expires_at, created_by_user_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, created_at
		`, firmID, roleID, token, StatusPending, expiresAt, userID).Scan(&result.ID, &result.CreatedAt); err != nil {
			return fmt.Errorf("insert invite: %w", err)
		}

		changes := map[string]any{"roleId": roleID, "expiresAt": expiresAt}
		return auditlog.Write(ctx, tx, firmID, userID, result.ID, inviteAuditEntityType, createInviteAuditAction, changes)
	})
	if err != nil {
		return Invite{}, err
	}
	return result, nil
}

// List returns every invite firmID has ever generated, most recent first -
// the invites-management table on /settings/members. Owner-gated, same
// tier as Generate/Revoke: who has an outstanding invite link (and what
// role it grants) is exactly the kind of membership-shaping information
// internal/permission.CreateRole/DeleteRole already treat as owner-only,
// not the looser plain-membership gate internal/permission.ListMembers
// uses for the roster itself.
func List(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Invite, error) {
	var invites []Invite
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		owner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !owner {
			return ErrPermissionDenied
		}

		rows, err := tx.Query(ctx, `
			SELECT fi.id, fi.role_id, r.name, fi.status, fi.expires_at,
				fi.created_by_user_id, fi.created_at, fi.used_at, fi.used_by_user_id
			FROM firm_invites fi
			JOIN roles r ON r.id = fi.role_id
			WHERE fi.firm_id = $1
			ORDER BY fi.created_at DESC
		`, firmID)
		if err != nil {
			return fmt.Errorf("list invites: %w", err)
		}
		defer rows.Close()
		now := time.Now()
		for rows.Next() {
			var inv Invite
			inv.FirmID = firmID
			if err := rows.Scan(
				&inv.ID, &inv.RoleID, &inv.RoleName, &inv.Status, &inv.ExpiresAt,
				&inv.CreatedByUserID, &inv.CreatedAt, &inv.UsedAt, &inv.UsedByUserID,
			); err != nil {
				return err
			}
			inv.Expired = inv.Status == StatusPending && inv.ExpiresAt.Before(now)
			invites = append(invites, inv)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return invites, nil
}

// Revoke marks a pending invite as revoked - it stays in firm_invites
// (never deleted) as its own record of what was revoked and by/when,
// matching internal/permission.DeleteRole's own soft-state precedent
// wherever this codebase already prefers that over a hard delete.
// Owner-gated. Rejects an invite that is already used or revoked
// (ErrInviteNotPending) rather than silently no-op'ing.
func Revoke(ctx context.Context, pool *pgxpool.Pool, firmID, userID, inviteID uuid.UUID) error {
	return zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		owner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !owner {
			return ErrPermissionDenied
		}

		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM firm_invites WHERE id = $1 AND firm_id = $2`, inviteID, firmID).Scan(&status); err != nil {
			if err == pgx.ErrNoRows {
				return ErrInviteNotFound
			}
			return fmt.Errorf("look up invite: %w", err)
		}
		if status != StatusPending {
			return ErrInviteNotPending
		}

		if _, err := tx.Exec(ctx, `UPDATE firm_invites SET status = $1 WHERE id = $2`, StatusRevoked, inviteID); err != nil {
			return fmt.Errorf("revoke invite: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, inviteID, inviteAuditEntityType, revokeInviteAuditAction, nil)
	})
}

// Info is what Lookup returns for the public, unauthenticated /invite/{token}
// page: enough to render "You've been invited to join <FirmName> as
// <RoleName>" (when Status is StatusPending and not Expired) or a specific
// reason it can't be accepted, without ever requiring the visitor to
// already have firm context - see this package's doc comment and
// db.WithInviteTokenContext.
type Info struct {
	FirmName  string
	RoleName  string
	Status    string
	Expired   bool
	ExpiresAt time.Time
}

// Lookup resolves token to its Info, with no authentication and no firm
// context required from the caller - the whole point of the accept flow
// (see db.WithInviteTokenContext's doc comment). Returns ErrInviteNotFound
// if no row matches token at all; a matching but non-pending/expired row
// is still returned successfully (Info.Status/Expired describe why it
// can't be accepted) so the page can show a specific, correct message
// rather than treating every non-happy-path the same as "not found".
func Lookup(ctx context.Context, pool *pgxpool.Pool, token string) (Info, error) {
	var info Info
	var firmID uuid.UUID
	var roleID uuid.UUID
	err := zdb.WithInviteTokenContext(ctx, pool, token, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var expiresAt time.Time
		if err := tx.QueryRow(ctx, `
			SELECT firm_id, role_id, status, expires_at FROM firm_invites WHERE token = $1
		`, token).Scan(&firmID, &roleID, &status, &expiresAt); err != nil {
			if err == pgx.ErrNoRows {
				return ErrInviteNotFound
			}
			return fmt.Errorf("look up invite: %w", err)
		}
		info.Status = status
		info.Expired = status == StatusPending && expiresAt.Before(time.Now())
		info.ExpiresAt = expiresAt

		// firms is a global, non-RLS table (migrations/0001_core_schema.up.sql)
		// so this read needs no firm context at all - but roles below does,
		// so app.current_firm_id is set here regardless, the same
		// "set it once the ID becomes known mid-transaction" pattern
		// internal/wizard.CreateDefaultFirm uses for its own first insert.
		if err := tx.QueryRow(ctx, `SELECT name FROM firms WHERE id = $1`, firmID).Scan(&info.FirmName); err != nil {
			return fmt.Errorf("look up firm: %w", err)
		}

		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_firm_id', $1, true)", firmID.String()); err != nil {
			return fmt.Errorf("set firm context: %w", err)
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM roles WHERE id = $1`, roleID).Scan(&info.RoleName); err != nil {
			return fmt.Errorf("look up role: %w", err)
		}
		return nil
	})
	if err != nil {
		return Info{}, err
	}
	return info, nil
}

// Accept validates token (exists, unexpired, unused, not revoked) and, if
// valid, grants userID roleID within the invite's firm - the write-side
// counterpart to Lookup, run as one transaction so the pending-check and
// the state change (marking the invite used, inserting user_firm_roles)
// can never race apart (`SELECT ... FOR UPDATE` below locks the row for
// the duration). Like Accept's caller (internal/invite's HTTP handler),
// this takes no firmID parameter from the outside at all - the firm is
// entirely derived from the token itself, which is exactly why this
// function is safe to call for a userID that is not yet, and has never
// been, a member of any firm.
//
// ciaudit:ignore-firmid-check: this function derives firmID from the
// token itself (never accepts one as a parameter), and performs no
// IsMember/IsOwner check by design - accepting an invite is the one
// membership-granting action in this codebase that must work for a
// caller who is *not* a member of the target firm yet. Authorization here
// comes entirely from possessing the unguessable token (see
// migrations/0007_firm_invites.up.sql's policy comment), the same trust
// model Lookup already relies on.
func Accept(ctx context.Context, pool *pgxpool.Pool, token string, userID uuid.UUID) (Invite, error) {
	var result Invite
	err := zdb.WithInviteTokenContext(ctx, pool, token, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		var expiresAt time.Time
		if err := tx.QueryRow(ctx, `
			SELECT id, firm_id, role_id, status, expires_at
			FROM firm_invites WHERE token = $1
			FOR UPDATE
		`, token).Scan(&result.ID, &result.FirmID, &result.RoleID, &status, &expiresAt); err != nil {
			if err == pgx.ErrNoRows {
				return ErrInviteNotFound
			}
			return fmt.Errorf("look up invite: %w", err)
		}

		switch {
		case status == StatusUsed:
			return ErrInviteUsed
		case status == StatusRevoked:
			return ErrInviteRevoked
		case status == StatusPending && expiresAt.Before(time.Now()):
			return ErrInviteExpired
		case status != StatusPending:
			// Unreachable given the CHECK constraint's three values and
			// the two cases above, but fails closed rather than falling
			// through to "accept it anyway" if a fourth status value is
			// ever added to the schema without updating this switch.
			return ErrInviteNotPending
		}

		// The invite's own firm_id, now known - set app.current_firm_id
		// so the writes below (user_firm_roles, the invite's own UPDATE,
		// audit_log) satisfy every one of those tables' WITH CHECK
		// clauses, the same manual "set the session setting once the ID
		// is known mid-transaction" sequencing
		// internal/wizard.CreateDefaultFirm uses for its own first
		// insert - see migrations/0007_firm_invites.up.sql's policy
		// comment for why this is a second, separate step from the
		// token-scoped read above rather than something WithFirmContext
		// could have set up front (firmID isn't known up front here).
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_firm_id', $1, true)", result.FirmID.String()); err != nil {
			return fmt.Errorf("set firm context: %w", err)
		}

		if err := tx.QueryRow(ctx, `SELECT name FROM roles WHERE id = $1`, result.RoleID).Scan(&result.RoleName); err != nil {
			return fmt.Errorf("look up role: %w", err)
		}

		// ON CONFLICT DO NOTHING: user_firm_roles' UNIQUE constraint is
		// (user_id, firm_id, role_id) - if the accepter already somehow
		// holds this exact role in this firm (re-accepting the same
		// invite link twice in quick succession before the first request
		// finished, for instance), this is a harmless no-op rather than
		// a spurious error, while the invite is still marked used below
		// either way.
		if _, err := tx.Exec(ctx, `
			INSERT INTO user_firm_roles (firm_id, user_id, role_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id, firm_id, role_id) DO NOTHING
		`, result.FirmID, userID, result.RoleID); err != nil {
			return fmt.Errorf("insert user_firm_roles: %w", err)
		}

		now := time.Now()
		if _, err := tx.Exec(ctx, `
			UPDATE firm_invites SET status = $1, used_at = $2, used_by_user_id = $3 WHERE id = $4
		`, StatusUsed, now, userID, result.ID); err != nil {
			return fmt.Errorf("mark invite used: %w", err)
		}
		result.Status = StatusUsed
		result.UsedAt = &now
		result.UsedByUserID = &userID
		result.ExpiresAt = expiresAt

		changes := map[string]any{"roleId": result.RoleID, "userId": userID}
		return auditlog.Write(ctx, tx, result.FirmID, userID, result.ID, inviteAuditEntityType, acceptInviteAuditAction, changes)
	})
	if err != nil {
		return Invite{}, err
	}
	return result, nil
}
