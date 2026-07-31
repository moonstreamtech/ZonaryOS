// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package platformadmin_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/platformadmin"
	"github.com/moonstreamtech/ZonaryOS/internal/wizard"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// These tests exercise internal/platformadmin against a real Postgres
// instance - RLS isolation can't be meaningfully faked, same convention as
// every other package's integration tests in this codebase (see e.g.
// internal/auditlog/auditlog_integration_test.go). Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run platformadmin integration tests")
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

func seedUser(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, keycloakSubject, email string) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, email, "Test User").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return userID
}

// TestListFirms_ReturnsMetadataAcrossMultipleFirms is test requirement 1:
// an allowlisted caller sees correct firm metadata (name, workflow
// definition count, member count) across more than one firm.
func TestListFirms_ReturnsMetadataAcrossMultipleFirms(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerA := seedUser(ctx, t, adminPool, "owner-a", "owner-a@example.com")
	firmA, err := wizard.CreateDefaultFirm(ctx, appPool, ownerA, "Firm A")
	if err != nil {
		t.Fatalf("CreateDefaultFirm A: %v", err)
	}

	ownerB := seedUser(ctx, t, adminPool, "owner-b", "owner-b@example.com")
	firmB, err := wizard.CreateDefaultFirm(ctx, appPool, ownerB, "Firm B")
	if err != nil {
		t.Fatalf("CreateDefaultFirm B: %v", err)
	}
	// Add a second member to firm B so its member count (2) differs from
	// firm A's (1), proving counts are genuinely per-firm and not just
	// always "1".
	secondMemberB := seedUser(ctx, t, adminPool, "member-b2", "member-b2@example.com")
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)
	`, firmB.FirmID, secondMemberB, firmB.RoleID); err != nil {
		t.Fatalf("seed second member of firm B: %v", err)
	}

	adminUserID := seedUser(ctx, t, adminPool, "platform-admin", "admin@zonaryos.com")
	allow := platformadmin.NewAllowlist([]string{"admin@zonaryos.com"})
	caller := identity.Identity{Subject: "platform-admin", Email: "admin@zonaryos.com"}
	_ = adminUserID

	results, err := platformadmin.ListFirms(ctx, appPool, allow, caller)
	if err != nil {
		t.Fatalf("ListFirms: %v", err)
	}

	byID := map[uuid.UUID]platformadmin.FirmMetadata{}
	for _, r := range results {
		byID[r.FirmID] = r
	}

	gotA, ok := byID[firmA.FirmID]
	if !ok {
		t.Fatalf("expected firm A (%s) in results, got %+v", firmA.FirmID, results)
	}
	if gotA.Name != "Firm A" {
		t.Errorf("firm A: expected name %q, got %q", "Firm A", gotA.Name)
	}
	// CreateDefaultFirm seeds two workflow definitions (stock-to-sale,
	// customer pipeline) per firm - see internal/wizard.CreateDefaultFirm.
	if gotA.WorkflowDefinitionCount != 2 {
		t.Errorf("firm A: expected 2 workflow definitions, got %d", gotA.WorkflowDefinitionCount)
	}
	if gotA.MemberCount != 1 {
		t.Errorf("firm A: expected 1 member, got %d", gotA.MemberCount)
	}

	gotB, ok := byID[firmB.FirmID]
	if !ok {
		t.Fatalf("expected firm B (%s) in results, got %+v", firmB.FirmID, results)
	}
	if gotB.Name != "Firm B" {
		t.Errorf("firm B: expected name %q, got %q", "Firm B", gotB.Name)
	}
	if gotB.WorkflowDefinitionCount != 2 {
		t.Errorf("firm B: expected 2 workflow definitions, got %d", gotB.WorkflowDefinitionCount)
	}
	if gotB.MemberCount != 2 {
		t.Errorf("firm B: expected 2 members, got %d", gotB.MemberCount)
	}
}

// TestListFirms_RejectsNonAllowlistedCallerAgainstRealDatabase is test
// requirement 2 exercised end-to-end against real Postgres (the unit test
// in allowlist_test.go proves the ordering with a poison-pill nil pool;
// this proves the same rejection holds with a real pool and real seeded
// firms present, i.e. there is real data ListFirms could have leaked and
// it still returns nothing).
func TestListFirms_RejectsNonAllowlistedCallerAgainstRealDatabase(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	owner := seedUser(ctx, t, adminPool, "owner-x", "owner-x@example.com")
	if _, err := wizard.CreateDefaultFirm(ctx, appPool, owner, "Firm X"); err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	allow := platformadmin.NewAllowlist([]string{"admin@zonaryos.com"})
	notAdmin := identity.Identity{Subject: "owner-x", Email: "owner-x@example.com"}

	results, err := platformadmin.ListFirms(ctx, appPool, allow, notAdmin)
	if err == nil {
		t.Fatalf("expected an error rejecting the non-allowlisted caller, got results: %+v", results)
	}
	if results != nil {
		t.Errorf("expected nil results on rejection, got %+v", results)
	}
}

// TestListFirms_DoesNotLeakTenantBusinessData is test requirement 3: an
// adversarial test proving the platform-admin query path cannot surface
// tenant business-data-table contents, not merely that the happy path
// looks narrow. It seeds a workflow instance and an audit_log entry
// carrying an obviously sensitive, uniquely identifiable payload, then
// asserts two independent things that would each fail if someone later
// widened this package's query carelessly:
//
//  1. FirmMetadata's field set is exactly the five documented metadata
//     fields (via reflection) - a future change that added, say, an
//     `Instances []map[string]any` field to smuggle business data through
//     would fail this assertion even before it got populated.
//  2. The JSON-marshaled result of a real ListFirms call, across firms
//     that do have sensitive business data sitting in workflow_instances
//     and audit_log, contains none of that data - proven by asserting the
//     sensitive marker strings are entirely absent from the output.
func TestListFirms_DoesNotLeakTenantBusinessData(t *testing.T) {
	// (1) Struct shape check.
	wantFields := []string{"FirmID", "Name", "CreatedAt", "WorkflowDefinitionCount", "MemberCount"}
	typ := reflect.TypeOf(platformadmin.FirmMetadata{})
	if typ.NumField() != len(wantFields) {
		t.Fatalf("FirmMetadata has %d fields, want exactly %d (%v) - a new field is exactly the kind of change that could smuggle tenant business data through this package's response; if it's legitimate, this test must be updated deliberately alongside it",
			typ.NumField(), len(wantFields), wantFields)
	}
	for i, name := range wantFields {
		if typ.Field(i).Name != name {
			t.Fatalf("FirmMetadata field %d: expected %q, got %q", i, name, typ.Field(i).Name)
		}
	}

	// (2) Live-data leakage check.
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	const secretItemMarker = "SUPER-SECRET-STOCK-ITEM-9f3c1a"
	const secretAuditMarker = "TOP-SECRET-AUDIT-DETAIL-4b7e2d"

	owner := seedUser(ctx, t, adminPool, "owner-y", "owner-y@example.com")
	firm, err := wizard.CreateDefaultFirm(ctx, appPool, owner, "Firm Y")
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	// Real tenant business data: a workflow instance whose payload
	// contains the marker.
	if _, err := workflow.CreateInstance(ctx, appPool, firm.FirmID, owner, firm.StockToSaleDefinitionID, map[string]any{
		"item":     secretItemMarker,
		"quantity": 1,
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Real audit_log content (a data-change entry, not the platform-admin
	// view-log entry this package itself writes) carrying the other
	// marker.
	if err := zdb.WithFirmContext(ctx, appPool, firm.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		return auditlog.Write(ctx, tx, firm.FirmID, owner, firm.FirmID, "firm", "test_sensitive_action", map[string]any{
			"detail": secretAuditMarker,
		})
	}); err != nil {
		t.Fatalf("seed sensitive audit_log entry: %v", err)
	}

	allow := platformadmin.NewAllowlist([]string{"admin@zonaryos.com"})
	caller := identity.Identity{Subject: "platform-admin", Email: "admin@zonaryos.com"}

	results, err := platformadmin.ListFirms(ctx, appPool, allow, caller)
	if err != nil {
		t.Fatalf("ListFirms: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one firm in results")
	}

	blob, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("marshal results: %v", err)
	}
	got := string(blob)

	if strings.Contains(got, secretItemMarker) {
		t.Errorf("ListFirms output leaked workflow_instances business data: found %q in %s", secretItemMarker, got)
	}
	if strings.Contains(got, secretAuditMarker) {
		t.Errorf("ListFirms output leaked audit_log content: found %q in %s", secretAuditMarker, got)
	}
}

// TestListFirms_WritesAuditLogEntryPerAccessedFirm is test requirement 4:
// every allowlisted access is logged. Because the read is inherently
// cross-firm, this package logs into each visited firm's own audit_log
// (see platformadmin.go's doc comment on firmCountsAndAuditTx for why that
// - rather than inventing a firm-less "platform" audit event - is what
// this package lands on) - so this asserts a "platform_admin_metadata_view"
// entry, attributed to the platform admin's resolved user ID, appears in
// every firm that was read.
func TestListFirms_WritesAuditLogEntryPerAccessedFirm(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	ownerA := seedUser(ctx, t, adminPool, "owner-audit-a", "owner-audit-a@example.com")
	firmA, err := wizard.CreateDefaultFirm(ctx, appPool, ownerA, "Firm Audit A")
	if err != nil {
		t.Fatalf("CreateDefaultFirm A: %v", err)
	}
	ownerB := seedUser(ctx, t, adminPool, "owner-audit-b", "owner-audit-b@example.com")
	firmB, err := wizard.CreateDefaultFirm(ctx, appPool, ownerB, "Firm Audit B")
	if err != nil {
		t.Fatalf("CreateDefaultFirm B: %v", err)
	}

	allow := platformadmin.NewAllowlist([]string{"admin@zonaryos.com"})
	caller := identity.Identity{Subject: "platform-admin", Email: "admin@zonaryos.com"}

	if _, err := platformadmin.ListFirms(ctx, appPool, allow, caller); err != nil {
		t.Fatalf("ListFirms: %v", err)
	}

	adminUserID, err := identity.ResolveOrCreateUser(ctx, appPool, caller)
	if err != nil {
		t.Fatalf("resolve admin user id: %v", err)
	}

	for _, firm := range []struct {
		name   string
		firmID uuid.UUID
		owner  uuid.UUID
	}{
		{"Firm Audit A", firmA.FirmID, ownerA},
		{"Firm Audit B", firmB.FirmID, ownerB},
	} {
		entries, err := auditlog.List(ctx, appPool, firm.firmID, firm.owner)
		if err != nil {
			t.Fatalf("List audit log for %s: %v", firm.name, err)
		}
		var found bool
		for _, e := range entries {
			if e.EntityType == "firm" && e.Action == "platform_admin_metadata_view" &&
				e.EntityID == firm.firmID && e.UserID == adminUserID {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: expected a platform_admin_metadata_view audit_log entry attributed to the platform admin, got entries: %+v", firm.name, entries)
		}
	}
}
