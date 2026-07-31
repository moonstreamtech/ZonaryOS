// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package auditlog_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/wizard"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// These tests exercise Vision §3's Audit Trail Infrastructure against a
// real Postgres instance - RLS isolation and permission enforcement can't
// be meaningfully faked, same convention as every other package's
// integration tests in this codebase. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run auditlog integration tests")
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

// seedRoleWithoutAuditAccess creates a real member of firmID who holds no
// permissions at all - not through the wizard, which always grants its
// owner role audit.log.read (see internal/wizard.CreateDefaultFirm) - so
// tests can exercise the "member but not authorized" path distinctly from
// "not a member at all".
func seedRoleWithoutAuditAccess(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID, keycloakSubject string) uuid.UUID {
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

// TestList_ReportsWriteAndViewEntriesWithAttribution covers item 1 (data
// changes) and item 2 (view/read logging) of this PR: creating a firm and
// a stock instance produces write entries; listing that firm's stock
// (internal/workflow.ListInstances, the one wired-up read path) produces a
// view entry - all attributed to the acting user.
func TestList_ReportsWriteAndViewEntriesWithAttribution(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerUserID := seedUser(ctx, t, adminPool, "owner-1")

	firm, err := wizard.CreateDefaultFirm(ctx, appPool, ownerUserID, "Acme Trading Co.")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	instanceID, err := workflow.CreateInstance(ctx, appPool, firm.FirmID, ownerUserID, firm.StockToSaleDefinitionID, map[string]any{
		"item":     "widget",
		"quantity": 3,
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := workflow.ListInstances(ctx, appPool, firm.FirmID, ownerUserID, firm.StockToSaleDefinitionID, workflow.ListInstancesOptions{}); err != nil {
		t.Fatalf("ListInstances: %v", err)
	}

	entries, err := auditlog.List(ctx, appPool, firm.FirmID, ownerUserID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var sawFirmCreate, sawInstanceCreate, sawListView bool
	for _, e := range entries {
		if e.UserID != ownerUserID {
			t.Errorf("expected every entry attributed to %v, got %v on entity_type %q", ownerUserID, e.UserID, e.EntityType)
		}
		switch {
		case e.EntityType == "firm" && e.Action == "create" && e.EntityID == firm.FirmID:
			sawFirmCreate = true
		case e.EntityType == "workflow_instance" && e.Action == "create" && e.EntityID == instanceID:
			sawInstanceCreate = true
		case e.EntityType == "workflow_instance_list" && e.Action == "view" && e.EntityID == firm.StockToSaleDefinitionID:
			sawListView = true
		}
	}
	if !sawFirmCreate {
		t.Error("expected a firm-creation audit_log entry")
	}
	if !sawInstanceCreate {
		t.Error("expected a stock-instance-creation audit_log entry")
	}
	if !sawListView {
		t.Error("expected a workflow_instance_list view-log entry from ListInstances")
	}
}

// TestList_CrossFirmIsolation covers the "a firm cannot read another
// firm's audit trail" requirement - same membership-check discipline as
// docs/OPEN_POINTS.md item 37's audit.
func TestList_CrossFirmIsolation(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerA := seedUser(ctx, t, adminPool, "owner-a")
	firmA, err := wizard.CreateDefaultFirm(ctx, appPool, ownerA, "Firm A")
	if err != nil {
		t.Fatalf("CreateDefaultFirm A: %v", err)
	}

	ownerB := seedUser(ctx, t, adminPool, "owner-b")
	firmB, err := wizard.CreateDefaultFirm(ctx, appPool, ownerB, "Firm B")
	if err != nil {
		t.Fatalf("CreateDefaultFirm B: %v", err)
	}

	// ownerB is a real owner of firm B, but not a member of firm A at all -
	// supplying firm A's genuine ID must not distinguish "real firm I'm not
	// in" from "unknown firm".
	if _, err := auditlog.List(ctx, appPool, firmA.FirmID, ownerB); !errors.Is(err, auditlog.ErrFirmNotFound) {
		t.Fatalf("expected ErrFirmNotFound, got: %v", err)
	}

	// Sanity: ownerB can read firm B's own log.
	if _, err := auditlog.List(ctx, appPool, firmB.FirmID, ownerB); err != nil {
		t.Fatalf("expected firm B's owner to read firm B's own audit log, got: %v", err)
	}
}

// TestList_RequiresReadPermission covers "non-owner/non-auditor roles
// cannot read the audit log at all" - a real member of the firm, holding
// no permissions (in particular not auditlog.ReadPermission), is rejected.
func TestList_RequiresReadPermission(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	owner := seedUser(ctx, t, adminPool, "owner-c")
	firm, err := wizard.CreateDefaultFirm(ctx, appPool, owner, "Firm C")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	salesUserID := seedRoleWithoutAuditAccess(ctx, t, adminPool, firm.FirmID, "sales-c")

	if _, err := auditlog.List(ctx, appPool, firm.FirmID, salesUserID); !errors.Is(err, auditlog.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got: %v", err)
	}
}

// TestRegisterReadPermissionTx_IsIdempotent exercises the ON CONFLICT DO
// NOTHING paths directly - calling it twice for the same role must not
// error or duplicate the grant.
func TestRegisterReadPermissionTx_IsIdempotent(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm D') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	var roleID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO roles (firm_id, key, name, is_owner) VALUES ($1, 'owner', 'Owner', true) RETURNING id
	`, firmID).Scan(&roleID); err != nil {
		t.Fatalf("seed role: %v", err)
	}

	register := func() error {
		return zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
			return auditlog.RegisterReadPermissionTx(ctx, tx, firmID, roleID)
		})
	}
	if err := register(); err != nil {
		t.Fatalf("first RegisterReadPermissionTx: %v", err)
	}
	if err := register(); err != nil {
		t.Fatalf("second RegisterReadPermissionTx should be idempotent, got: %v", err)
	}

	var grantCount int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM role_permissions WHERE role_id = $1 AND permission_key = $2
	`, roleID, auditlog.ReadPermission).Scan(&grantCount); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if grantCount != 1 {
		t.Errorf("expected exactly 1 grant row, got %d", grantCount)
	}
}

// TestPurgeOlderThan_DeletesOnlyRowsOlderThanCutoff exercises the retention
// mechanism's mechanics without asserting any particular retention
// duration - docs/OPEN_POINTS.md item 33 leaves the actual period an open
// legal question; this only proves PurgeOlderThan deletes what's older
// than whatever cutoff its caller supplies, and leaves everything else
// firm-scoped and RLS-respecting.
func TestPurgeOlderThan_DeletesOnlyRowsOlderThanCutoff(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	userID := seedUser(ctx, t, adminPool, "purge-user")
	firm, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Firm E")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	oldTime := time.Now().Add(-30 * 24 * time.Hour)
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO audit_log (firm_id, user_id, entity_type, entity_id, action, changes, occurred_at)
		VALUES ($1, $2, 'test_entity', gen_random_uuid(), 'test', '{}'::jsonb, $3)
	`, firm.FirmID, userID, oldTime); err != nil {
		t.Fatalf("seed old audit_log row: %v", err)
	}

	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	deleted, err := auditlog.PurgeOlderThan(ctx, appPool, cutoff)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if deleted < 1 {
		t.Errorf("expected at least 1 row deleted, got %d", deleted)
	}

	var remainingOld int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log WHERE entity_type = 'test_entity' AND occurred_at < $1
	`, cutoff).Scan(&remainingOld); err != nil {
		t.Fatalf("count remaining old rows: %v", err)
	}
	if remainingOld != 0 {
		t.Errorf("expected the old test row to be purged, %d remain", remainingOld)
	}

	// The firm-creation entry (recent, well after cutoff) must survive.
	var remainingRecent int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log WHERE firm_id = $1 AND entity_type = 'firm'
	`, firm.FirmID).Scan(&remainingRecent); err != nil {
		t.Fatalf("count remaining recent rows: %v", err)
	}
	if remainingRecent != 1 {
		t.Errorf("expected the recent firm-creation row to survive purging, got %d", remainingRecent)
	}
}
