// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package invite_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/invite"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/wizard"
)

// These tests exercise internal/invite against a real Postgres instance -
// RLS isolation, the token-based read carve-out
// (migrations/0007_firm_invites.up.sql), and single-use/expiry
// enforcement can't be meaningfully faked - same convention as every
// other package's integration tests in this codebase (see
// internal/firm/firm_integration_test.go). Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run invite integration tests")
	}
	return adminDSN, appDSN
}

func setupTest(t *testing.T) (adminPool, appPool *pgxpool.Pool) {
	t.Helper()
	adminDSN, appDSN := testPools(t)
	ctx := context.Background()

	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	adminPool, err := zdb.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)
	if _, err := adminPool.Exec(ctx, `
		TRUNCATE firms, users, roles, role_permissions, user_firm_roles,
			workflow_definitions, workflow_states, workflow_transitions,
			workflow_instances, audit_log, permissions, firm_invites CASCADE
	`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err = zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(appPool.Close)

	return adminPool, appPool
}

func seedUser(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, keycloakSubject string) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test User").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return userID
}

func seedNonOwnerMember(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID, keycloakSubject string) uuid.UUID {
	t.Helper()
	var roleID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO roles (firm_id, key, name) VALUES ($1, 'sales', 'Sales') RETURNING id
	`, firmID).Scan(&roleID); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	userID := seedUser(ctx, t, adminPool, keycloakSubject)
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)
	`, firmID, userID, roleID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	return userID
}

// TestGenerate_OwnerCanGenerateInvite covers the happy path: an owner
// generates an invite for a real role in their own firm, and the
// resulting row's fields match what was asked for.
func TestGenerate_OwnerCanGenerateInvite(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerID := seedUser(ctx, t, adminPool, "owner-generate")
	created, err := wizard.CreateDefaultFirm(ctx, appPool, ownerID, "Invite Firm")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}
	roleID, err := permission.CreateRole(ctx, appPool, created.FirmID, ownerID, "sales", "Sales")
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}

	inv, err := invite.Generate(ctx, appPool, created.FirmID, ownerID, roleID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if inv.Token == "" {
		t.Error("expected a non-empty token")
	}
	if inv.Status != invite.StatusPending {
		t.Errorf("expected status %q, got %q", invite.StatusPending, inv.Status)
	}
	if inv.RoleID != roleID {
		t.Errorf("expected roleID %v, got %v", roleID, inv.RoleID)
	}
	if !inv.ExpiresAt.After(time.Now().Add(6*24*time.Hour)) {
		t.Errorf("expected expiresAt roughly 7 days out, got %v", inv.ExpiresAt)
	}
}

// TestGenerate_NonOwnerMemberIsDenied mirrors
// internal/firm's TestUpdateName_NonOwnerMemberIsDenied for invite
// generation: a real, non-owner member of the firm cannot generate one.
func TestGenerate_NonOwnerMemberIsDenied(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerID := seedUser(ctx, t, adminPool, "owner-generate-denied")
	created, err := wizard.CreateDefaultFirm(ctx, appPool, ownerID, "Invite Firm")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}
	memberID := seedNonOwnerMember(ctx, t, adminPool, created.FirmID, "member-generate-denied")

	var roleID uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT id FROM roles WHERE firm_id = $1 AND key = 'sales'`, created.FirmID).Scan(&roleID); err != nil {
		t.Fatalf("look up seeded role: %v", err)
	}

	if _, err := invite.Generate(ctx, appPool, created.FirmID, memberID, roleID); !errors.Is(err, invite.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for a non-owner member, got: %v", err)
	}
}

// TestGenerate_RoleFromDifferentFirmIsRejected is the brief's required
// cross-firm role-ID confusion check: generating an invite for firm A
// using a real role ID that belongs to firm B must be rejected, not
// silently accepted or misattributed - see ErrRoleNotFound's doc comment
// for why roles' own RLS policy makes this structural, not a bolted-on
// application check.
func TestGenerate_RoleFromDifferentFirmIsRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerAID := seedUser(ctx, t, adminPool, "owner-a-cross-firm")
	firmA, err := wizard.CreateDefaultFirm(ctx, appPool, ownerAID, "Firm A")
	if err != nil {
		t.Fatalf("CreateDefaultFirm A: %v", err)
	}
	ownerBID := seedUser(ctx, t, adminPool, "owner-b-cross-firm")
	firmB, err := wizard.CreateDefaultFirm(ctx, appPool, ownerBID, "Firm B")
	if err != nil {
		t.Fatalf("CreateDefaultFirm B: %v", err)
	}
	roleBID, err := permission.CreateRole(ctx, appPool, firmB.FirmID, ownerBID, "b-role", "B Role")
	if err != nil {
		t.Fatalf("CreateRole in firm B: %v", err)
	}

	if _, err := invite.Generate(ctx, appPool, firmA.FirmID, ownerAID, roleBID); !errors.Is(err, invite.ErrRoleNotFound) {
		t.Fatalf("expected ErrRoleNotFound when firm A's owner supplies a real role ID from firm B, got: %v", err)
	}

	// Nothing should have been inserted at all.
	var count int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM firm_invites WHERE firm_id = $1`, firmA.FirmID).Scan(&count); err != nil {
		t.Fatalf("count invites: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no invite to be created, found %d", count)
	}
}

// TestAccept_GrantsTheCorrectRoleAndMarksTheInviteUsed is the core accept
// flow: a brand-new user (not a member of any firm) accepts a valid
// invite and ends up with exactly the right user_firm_roles row, and the
// invite itself flips to 'used'.
func TestAccept_GrantsTheCorrectRoleAndMarksTheInviteUsed(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerID := seedUser(ctx, t, adminPool, "owner-accept")
	created, err := wizard.CreateDefaultFirm(ctx, appPool, ownerID, "Accept Firm")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}
	roleID, err := permission.CreateRole(ctx, appPool, created.FirmID, ownerID, "sales", "Sales")
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	inv, err := invite.Generate(ctx, appPool, created.FirmID, ownerID, roleID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	inviteeID := seedUser(ctx, t, adminPool, "invitee-accept")

	// Sanity: the invitee is not a member of anything yet.
	member, err := permission.CheckMembership(ctx, appPool, created.FirmID, inviteeID)
	if err != nil {
		t.Fatalf("CheckMembership before accept: %v", err)
	}
	if member {
		t.Fatal("expected the invitee to not be a member before accepting")
	}

	accepted, err := invite.Accept(ctx, appPool, inv.Token, inviteeID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if accepted.FirmID != created.FirmID || accepted.RoleID != roleID {
		t.Errorf("expected firmID %v / roleID %v, got firmID %v / roleID %v", created.FirmID, roleID, accepted.FirmID, accepted.RoleID)
	}

	var storedRoleID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		SELECT role_id FROM user_firm_roles WHERE firm_id = $1 AND user_id = $2
	`, created.FirmID, inviteeID).Scan(&storedRoleID); err != nil {
		t.Fatalf("query user_firm_roles: %v", err)
	}
	if storedRoleID != roleID {
		t.Errorf("expected the invitee to hold role %v, got %v", roleID, storedRoleID)
	}

	var status string
	var usedByUserID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		SELECT status, used_by_user_id FROM firm_invites WHERE id = $1
	`, inv.ID).Scan(&status, &usedByUserID); err != nil {
		t.Fatalf("query invite status: %v", err)
	}
	if status != invite.StatusUsed {
		t.Errorf("expected status %q, got %q", invite.StatusUsed, status)
	}
	if usedByUserID != inviteeID {
		t.Errorf("expected used_by_user_id %v, got %v", inviteeID, usedByUserID)
	}
}

// TestAccept_SecondAcceptOnTheSameTokenFails is the brief's required
// single-use enforcement check: a second Accept call on an already-used
// token must fail cleanly, not silently re-grant or no-op successfully.
func TestAccept_SecondAcceptOnTheSameTokenFails(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerID := seedUser(ctx, t, adminPool, "owner-double-accept")
	created, err := wizard.CreateDefaultFirm(ctx, appPool, ownerID, "Double Accept Firm")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}
	roleID, err := permission.CreateRole(ctx, appPool, created.FirmID, ownerID, "sales", "Sales")
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	inv, err := invite.Generate(ctx, appPool, created.FirmID, ownerID, roleID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	firstUserID := seedUser(ctx, t, adminPool, "invitee-first-accept")
	if _, err := invite.Accept(ctx, appPool, inv.Token, firstUserID); err != nil {
		t.Fatalf("first Accept: %v", err)
	}

	secondUserID := seedUser(ctx, t, adminPool, "invitee-second-accept")
	if _, err := invite.Accept(ctx, appPool, inv.Token, secondUserID); !errors.Is(err, invite.ErrInviteUsed) {
		t.Fatalf("expected ErrInviteUsed on a second accept attempt, got: %v", err)
	}

	member, err := permission.CheckMembership(ctx, appPool, created.FirmID, secondUserID)
	if err != nil {
		t.Fatalf("CheckMembership: %v", err)
	}
	if member {
		t.Error("expected the second acceptor to NOT have been granted membership")
	}
}

// TestAccept_ExpiredInviteIsRejected is the brief's required expiry
// enforcement check: an otherwise well-formed, never-used, never-revoked
// invite whose expires_at has passed must still be rejected.
func TestAccept_ExpiredInviteIsRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerID := seedUser(ctx, t, adminPool, "owner-expired")
	created, err := wizard.CreateDefaultFirm(ctx, appPool, ownerID, "Expired Firm")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}
	roleID, err := permission.CreateRole(ctx, appPool, created.FirmID, ownerID, "sales", "Sales")
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	inv, err := invite.Generate(ctx, appPool, created.FirmID, ownerID, roleID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Force expiry directly - Generate always uses DefaultExpiry (7 days),
	// so this is the only way to exercise the expired branch without
	// waiting a week.
	if _, err := adminPool.Exec(ctx, `
		UPDATE firm_invites SET expires_at = $1 WHERE id = $2
	`, time.Now().Add(-1*time.Hour), inv.ID); err != nil {
		t.Fatalf("force-expire invite: %v", err)
	}

	inviteeID := seedUser(ctx, t, adminPool, "invitee-expired")
	if _, err := invite.Accept(ctx, appPool, inv.Token, inviteeID); !errors.Is(err, invite.ErrInviteExpired) {
		t.Fatalf("expected ErrInviteExpired, got: %v", err)
	}

	member, err := permission.CheckMembership(ctx, appPool, created.FirmID, inviteeID)
	if err != nil {
		t.Fatalf("CheckMembership: %v", err)
	}
	if member {
		t.Error("expected no membership to be granted for an expired invite")
	}

	// Lookup should also report it as expired, not simply "pending".
	info, err := invite.Lookup(ctx, appPool, inv.Token)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !info.Expired {
		t.Error("expected Lookup to report the invite as expired")
	}
	if info.Status != invite.StatusPending {
		t.Errorf("expected the persisted status to still be %q (expiry is derived, not persisted), got %q", invite.StatusPending, info.Status)
	}
}

// TestRevoke_RevokedInviteCanNoLongerBeAccepted is the brief's required
// revocation check.
func TestRevoke_RevokedInviteCanNoLongerBeAccepted(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerID := seedUser(ctx, t, adminPool, "owner-revoke")
	created, err := wizard.CreateDefaultFirm(ctx, appPool, ownerID, "Revoke Firm")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}
	roleID, err := permission.CreateRole(ctx, appPool, created.FirmID, ownerID, "sales", "Sales")
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	inv, err := invite.Generate(ctx, appPool, created.FirmID, ownerID, roleID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if err := invite.Revoke(ctx, appPool, created.FirmID, ownerID, inv.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	var status string
	if err := adminPool.QueryRow(ctx, `SELECT status FROM firm_invites WHERE id = $1`, inv.ID).Scan(&status); err != nil {
		t.Fatalf("query invite status: %v", err)
	}
	if status != invite.StatusRevoked {
		t.Errorf("expected status %q after revoke, got %q (row should not be deleted, only marked revoked)", invite.StatusRevoked, status)
	}

	inviteeID := seedUser(ctx, t, adminPool, "invitee-revoked")
	if _, err := invite.Accept(ctx, appPool, inv.Token, inviteeID); !errors.Is(err, invite.ErrInviteRevoked) {
		t.Fatalf("expected ErrInviteRevoked, got: %v", err)
	}

	// Revoking an already-revoked (non-pending) invite again must fail
	// cleanly too, not silently succeed a second time.
	if err := invite.Revoke(ctx, appPool, created.FirmID, ownerID, inv.ID); !errors.Is(err, invite.ErrInviteNotPending) {
		t.Fatalf("expected ErrInviteNotPending on a second revoke, got: %v", err)
	}
}

// TestRevoke_NonOwnerMemberIsDenied mirrors the generation-side owner gate.
func TestRevoke_NonOwnerMemberIsDenied(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerID := seedUser(ctx, t, adminPool, "owner-revoke-denied")
	created, err := wizard.CreateDefaultFirm(ctx, appPool, ownerID, "Revoke Denied Firm")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}
	roleID, err := permission.CreateRole(ctx, appPool, created.FirmID, ownerID, "support", "Support")
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	inv, err := invite.Generate(ctx, appPool, created.FirmID, ownerID, roleID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	memberID := seedNonOwnerMember(ctx, t, adminPool, created.FirmID, "member-revoke-denied")

	if err := invite.Revoke(ctx, appPool, created.FirmID, memberID, inv.ID); !errors.Is(err, invite.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for a non-owner member, got: %v", err)
	}

	var status string
	if err := adminPool.QueryRow(ctx, `SELECT status FROM firm_invites WHERE id = $1`, inv.ID).Scan(&status); err != nil {
		t.Fatalf("query invite status: %v", err)
	}
	if status != invite.StatusPending {
		t.Errorf("expected the invite to remain pending after a denied revoke, got %q", status)
	}
}

// TestLookup_UnknownTokenIsNotFound covers Lookup's own not-found path,
// distinct from every other non-happy-path (used/revoked/expired), which
// Lookup returns as a successful response instead - see
// TestAccept_ExpiredInviteIsRejected's own Lookup assertion above.
func TestLookup_UnknownTokenIsNotFound(t *testing.T) {
	_, appPool := setupTest(t)
	ctx := context.Background()

	if _, err := invite.Lookup(ctx, appPool, "this-token-was-never-generated"); !errors.Is(err, invite.ErrInviteNotFound) {
		t.Fatalf("expected ErrInviteNotFound, got: %v", err)
	}
}

// TestList_OwnerSeesGeneratedInvites is a basic sanity check on List: the
// owner who generated an invite can see it back via List, with the
// correct role name joined in.
func TestList_OwnerSeesGeneratedInvites(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerID := seedUser(ctx, t, adminPool, "owner-list")
	created, err := wizard.CreateDefaultFirm(ctx, appPool, ownerID, "List Firm")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}
	roleID, err := permission.CreateRole(ctx, appPool, created.FirmID, ownerID, "sales", "Sales")
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	inv, err := invite.Generate(ctx, appPool, created.FirmID, ownerID, roleID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	invites, err := invite.List(ctx, appPool, created.FirmID, ownerID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(invites) != 1 {
		t.Fatalf("expected exactly 1 invite, got %d", len(invites))
	}
	if invites[0].ID != inv.ID || invites[0].RoleName != "Sales" {
		t.Errorf("expected the generated invite (role %q) back, got %+v", "Sales", invites[0])
	}
}
