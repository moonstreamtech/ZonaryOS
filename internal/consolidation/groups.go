// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package consolidation is Part 1-3 of the multi-company/consolidation/
// group-accounting batch: firm_groups/firm_group_members (which firms
// belong to a group, and in what capacity), inter-company transfers
// (matching journal entries posted atomically into two member firms'
// books), and a consolidated P&L across a group's member firms with
// inter-company elimination. Kept as its own package, a sibling to
// internal/platformadmin rather than folded into it, because it does
// real writes (group membership, journal-entry-posting transfers) on top
// of platformadmin's existing read-only allowlist-gated surface - see
// internal/platformadmin/allowlist.go's own doc comment for why that
// package stays narrow.
//
// Auth model: every function here that creates/modifies a group,
// membership, or posts an inter-company transfer requires the caller's
// email to be on the SAME internal/platformadmin.Allowlist every other
// platform-admin-gated write in this codebase (internal/currency's
// CreateRate) already reuses - checked first, before any database
// access, same "off-allowlist looks like the endpoint doesn't exist"
// posture (404, not 403) as internal/platformadmin.handleListFirms and
// internal/currency.handleCreateRate. A firm-side caller only ever
// reaches GetFirmGroupForFirm, which is member-gated (not owner-gated,
// not platform-admin-gated) - see its own doc comment for the narrow,
// deliberately non-financial data it returns.
package consolidation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
)

// postgresUniqueViolation/postgresCheckViolation are the SQLSTATE codes
// Postgres raises for a UNIQUE/CHECK constraint violation - redeclared
// per package, not imported, the same convention
// internal/accounting.postgresUniqueViolation and
// internal/workflow.postgresUniqueViolation already establish.
const (
	postgresUniqueViolation = "23505"
	postgresCheckViolation  = "23514"
)

// FirmGroupRole is a member firm's role within its group - a closed set
// of three values, validated in Go (not a DB CHECK beyond the raw string
// list), the same "text column, application-validated" convention
// internal/accounting.AccountType already uses.
type FirmGroupRole string

const (
	FirmGroupRoleParent     FirmGroupRole = "parent"
	FirmGroupRoleSubsidiary FirmGroupRole = "subsidiary"
	FirmGroupRoleAffiliate  FirmGroupRole = "affiliate"
)

func (r FirmGroupRole) valid() bool {
	switch r {
	case FirmGroupRoleParent, FirmGroupRoleSubsidiary, FirmGroupRoleAffiliate:
		return true
	default:
		return false
	}
}

// FirmGroup is one platform-level group row.
type FirmGroup struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}

// FirmGroupMember is one firm_group_members row, as returned by
// AddGroupMember.
type FirmGroupMember struct {
	ID           uuid.UUID
	GroupID      uuid.UUID
	FirmID       uuid.UUID
	Role         FirmGroupRole
	OwnershipPct *string
	JoinedAt     time.Time
}

// FirmGroupMemberView is one group member as exposed to a READER (either
// a platform admin via ListFirmGroups, or a firm's own member via
// GetFirmGroupForFirm) - firm id/name/role/ownership_pct only, never any
// financial figure. Deliberately a different, narrower shape than
// FirmGroupMember: the write path (AddGroupMember) returns the full row
// including its own id/joined_at, which readers don't need.
type FirmGroupMemberView struct {
	FirmID       uuid.UUID
	FirmName     string
	Role         FirmGroupRole
	OwnershipPct *string
}

// FirmGroupDetail is a group plus its member firms, ListFirmGroups' and
// GetFirmGroupForFirm's shared return shape.
type FirmGroupDetail struct {
	FirmGroup
	Members []FirmGroupMemberView
}

// CreateFirmGroup creates a new, empty firm_groups row. Platform-admin
// only - firm_groups is a platform-level structure (this migration's own
// doc comment), so there is no firm-side equivalent to gate against.
func CreateFirmGroup(ctx context.Context, pool *pgxpool.Pool, allow platformadmin.Allowlist, caller identity.Identity, name string) (FirmGroup, error) {
	if !allow.Contains(caller.Email) {
		return FirmGroup{}, platformadmin.ErrNotPlatformAdmin
	}
	if name == "" {
		return FirmGroup{}, ErrInvalidGroup
	}

	var g FirmGroup
	if err := pool.QueryRow(ctx, `
		INSERT INTO firm_groups (name) VALUES ($1) RETURNING id, name, created_at
	`, name).Scan(&g.ID, &g.Name, &g.CreatedAt); err != nil {
		return FirmGroup{}, fmt.Errorf("insert firm group: %w", err)
	}
	return g, nil
}

// ListFirmGroups returns every firm_groups row with its member firms
// (id/name/role/ownership_pct) - the platform-admin firm-groups
// management page's data source. Platform-admin only.
func ListFirmGroups(ctx context.Context, pool *pgxpool.Pool, allow platformadmin.Allowlist, caller identity.Identity) ([]FirmGroupDetail, error) {
	if !allow.Contains(caller.Email) {
		return nil, platformadmin.ErrNotPlatformAdmin
	}

	rows, err := pool.Query(ctx, `SELECT id, name, created_at FROM firm_groups ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list firm groups: %w", err)
	}
	groups := []FirmGroupDetail{}
	index := make(map[uuid.UUID]int)
	for rows.Next() {
		var g FirmGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		index[g.ID] = len(groups)
		groups = append(groups, FirmGroupDetail{FirmGroup: g})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if len(groups) == 0 {
		return groups, nil
	}

	memberRows, err := pool.Query(ctx, `
		SELECT m.group_id, f.id, f.name, m.role, m.ownership_pct::text
		FROM firm_group_members m
		JOIN firms f ON f.id = m.member_firm_id
		ORDER BY f.name
	`)
	if err != nil {
		return nil, fmt.Errorf("list firm group members: %w", err)
	}
	defer memberRows.Close()
	for memberRows.Next() {
		var groupID uuid.UUID
		var v FirmGroupMemberView
		var roleStr string
		if err := memberRows.Scan(&groupID, &v.FirmID, &v.FirmName, &roleStr, &v.OwnershipPct); err != nil {
			return nil, err
		}
		v.Role = FirmGroupRole(roleStr)
		if idx, ok := index[groupID]; ok {
			groups[idx].Members = append(groups[idx].Members, v)
		}
	}
	if err := memberRows.Err(); err != nil {
		return nil, err
	}
	return groups, nil
}

// AddGroupMember adds firmID to groupID with the given role/ownership
// percentage. Platform-admin only - firm owners can VIEW that their firm
// belongs to a group (GetFirmGroupForFirm) but cannot modify group
// membership themselves, per this batch's own design brief.
//
// ciaudit:ignore-firmid-check: this intentionally does not call
// permission.IsMember/IsOwner for firmID - the caller is platform-admin
// staff (allow.Contains, checked first above), not a member of firmID at
// all, and membership is not what authorizes this write. Same reasoning
// internal/platformadmin.firmCountsAndAuditTx's own suppression already
// documents for a platform-admin caller touching a firm it doesn't
// belong to.
func AddGroupMember(ctx context.Context, pool *pgxpool.Pool, allow platformadmin.Allowlist, caller identity.Identity, groupID, firmID uuid.UUID, role FirmGroupRole, ownershipPct *string) (FirmGroupMember, error) {
	if !allow.Contains(caller.Email) {
		return FirmGroupMember{}, platformadmin.ErrNotPlatformAdmin
	}
	if !role.valid() {
		return FirmGroupMember{}, ErrInvalidGroup
	}

	var groupExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM firm_groups WHERE id = $1)`, groupID).Scan(&groupExists); err != nil {
		return FirmGroupMember{}, fmt.Errorf("check firm group exists: %w", err)
	}
	if !groupExists {
		return FirmGroupMember{}, ErrGroupNotFound
	}
	var firmExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM firms WHERE id = $1)`, firmID).Scan(&firmExists); err != nil {
		return FirmGroupMember{}, fmt.Errorf("check firm exists: %w", err)
	}
	if !firmExists {
		return FirmGroupMember{}, ErrFirmNotFound
	}

	var m FirmGroupMember
	var roleStr string
	err := pool.QueryRow(ctx, `
		INSERT INTO firm_group_members (group_id, member_firm_id, role, ownership_pct)
		VALUES ($1, $2, $3, $4)
		RETURNING id, group_id, member_firm_id, role, ownership_pct::text, joined_at
	`, groupID, firmID, string(role), ownershipPct).Scan(&m.ID, &m.GroupID, &m.FirmID, &roleStr, &m.OwnershipPct, &m.JoinedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case postgresUniqueViolation:
				return FirmGroupMember{}, ErrDuplicateMembership
			case postgresCheckViolation:
				return FirmGroupMember{}, ErrInvalidGroup
			}
		}
		return FirmGroupMember{}, fmt.Errorf("insert firm group member: %w", err)
	}
	m.Role = FirmGroupRole(roleStr)
	return m, nil
}

// GetFirmGroupForFirm returns the group (if any) firmID belongs to, and
// its fellow member firms' names/roles/ownership - never any financial
// data (Task's own explicit boundary: "a subsidiary shouldn't see the
// parent's books"). Returns (nil, nil) when firmID isn't in any group -
// not finding a group is the overwhelmingly common case for most firms
// and is not itself an error.
//
// Member-gated (not owner-gated): this codebase's convention for "read
// data about my own firm" is IsMember (see e.g.
// internal/accounting.ListAccounts) - reading which group your own firm
// belongs to is that same tier, not a structural firm decision the way
// modifying membership is (platform-admin only, see AddGroupMember).
func GetFirmGroupForFirm(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) (*FirmGroupDetail, error) {
	var detail *FirmGroupDetail
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var groupID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT group_id FROM firm_group_members WHERE member_firm_id = $1`, firmID).Scan(&groupID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("look up firm's group: %w", err)
		}

		var g FirmGroup
		if err := tx.QueryRow(ctx, `SELECT id, name, created_at FROM firm_groups WHERE id = $1`, groupID).Scan(&g.ID, &g.Name, &g.CreatedAt); err != nil {
			return fmt.Errorf("look up firm group: %w", err)
		}

		rows, err := tx.Query(ctx, `
			SELECT f.id, f.name, m.role, m.ownership_pct::text
			FROM firm_group_members m
			JOIN firms f ON f.id = m.member_firm_id
			WHERE m.group_id = $1
			ORDER BY f.name
		`, groupID)
		if err != nil {
			return fmt.Errorf("list group members: %w", err)
		}
		defer rows.Close()
		members := []FirmGroupMemberView{}
		for rows.Next() {
			var v FirmGroupMemberView
			var roleStr string
			if err := rows.Scan(&v.FirmID, &v.FirmName, &roleStr, &v.OwnershipPct); err != nil {
				return err
			}
			v.Role = FirmGroupRole(roleStr)
			members = append(members, v)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		detail = &FirmGroupDetail{FirmGroup: g, Members: members}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}
