// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package firm_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/firm"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/wizard"
)

// These tests exercise internal/firm against a real Postgres instance -
// RLS isolation and permission enforcement can't be meaningfully faked,
// same convention as every other package's integration tests in this
// codebase. Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL and
// ZONARYOS_TEST_APP_DATABASE_URL are set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run firm integration tests")
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
			workflow_instances, audit_log, permissions CASCADE
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

// seedNonOwnerMember mirrors internal/auditlog's seedRoleWithoutAuditAccess:
// a real member of firmID holding a plain, non-owner role - not through
// the wizard, which always makes its founder an owner.
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

func TestUpdateName_OwnerCanRenameTheFirm(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerID := seedUser(ctx, t, adminPool, "owner-rename")
	created, err := wizard.CreateDefaultFirm(ctx, appPool, ownerID, "Original Name")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	if err := firm.UpdateName(ctx, appPool, created.FirmID, ownerID, "  Renamed Firm  "); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}

	var storedName string
	if err := adminPool.QueryRow(ctx, `SELECT name FROM firms WHERE id = $1`, created.FirmID).Scan(&storedName); err != nil {
		t.Fatalf("query firm name: %v", err)
	}
	if storedName != "Renamed Firm" {
		t.Errorf("expected the name to be trimmed and stored as %q, got %q", "Renamed Firm", storedName)
	}

	entries, err := auditlog.List(ctx, appPool, created.FirmID, ownerID)
	if err != nil {
		t.Fatalf("auditlog.List: %v", err)
	}
	var sawRename bool
	for _, e := range entries {
		if e.EntityType == "firm" && e.Action == "update" && e.EntityID == created.FirmID {
			sawRename = true
			if e.Changes["name"] != "Renamed Firm" {
				t.Errorf("expected the audit entry's changes to record the new name, got %+v", e.Changes)
			}
		}
	}
	if !sawRename {
		t.Error("expected a firm-rename audit_log entry")
	}
}

func TestUpdateName_NonOwnerMemberIsDenied(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerID := seedUser(ctx, t, adminPool, "owner-nonowner-test")
	created, err := wizard.CreateDefaultFirm(ctx, appPool, ownerID, "Original Name")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}
	memberID := seedNonOwnerMember(ctx, t, adminPool, created.FirmID, "sub-firm-nonowner")

	if err := firm.UpdateName(ctx, appPool, created.FirmID, memberID, "Hostile Rename"); !errors.Is(err, firm.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for a non-owner member, got: %v", err)
	}

	var storedName string
	if err := adminPool.QueryRow(ctx, `SELECT name FROM firms WHERE id = $1`, created.FirmID).Scan(&storedName); err != nil {
		t.Fatalf("query firm name: %v", err)
	}
	if storedName != "Original Name" {
		t.Errorf("expected the name to be unchanged after a denied rename, got %q", storedName)
	}
}

func TestUpdateName_NonMemberIsDenied(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerAID := seedUser(ctx, t, adminPool, "owner-a-firm-test")
	firmA, err := wizard.CreateDefaultFirm(ctx, appPool, ownerAID, "Firm A")
	if err != nil {
		t.Fatalf("CreateDefaultFirm A: %v", err)
	}
	outsiderID := seedUser(ctx, t, adminPool, "owner-b-firm-test")
	if _, err := wizard.CreateDefaultFirm(ctx, appPool, outsiderID, "Firm B"); err != nil {
		t.Fatalf("CreateDefaultFirm B: %v", err)
	}

	if err := firm.UpdateName(ctx, appPool, firmA.FirmID, outsiderID, "Hostile Rename"); !errors.Is(err, firm.ErrFirmNotFound) {
		t.Fatalf("expected ErrFirmNotFound when an owner of a different firm supplies firm A's real ID, got: %v", err)
	}
}

func TestUpdateName_EmptyNameIsRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerID := seedUser(ctx, t, adminPool, "owner-empty-name-test")
	created, err := wizard.CreateDefaultFirm(ctx, appPool, ownerID, "Original Name")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	if err := firm.UpdateName(ctx, appPool, created.FirmID, ownerID, "   "); !errors.Is(err, firm.ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName for a whitespace-only name, got: %v", err)
	}
}
