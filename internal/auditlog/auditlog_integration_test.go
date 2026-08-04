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

	firm, err := wizard.CreateDefaultFirm(ctx, appPool, ownerUserID, "Acme Trading Co.", wizard.SeedSelection{Sells: true, TracksInventory: true, ManagesCRM: true})
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

	result, err := auditlog.List(ctx, appPool, firm.FirmID, ownerUserID, auditlog.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var sawFirmCreate, sawInstanceCreate, sawListView bool
	for _, e := range result.Entries {
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
	firmA, err := wizard.CreateDefaultFirm(ctx, appPool, ownerA, "Firm A", wizard.SeedSelection{Sells: true, TracksInventory: true, ManagesCRM: true})
	if err != nil {
		t.Fatalf("CreateDefaultFirm A: %v", err)
	}

	ownerB := seedUser(ctx, t, adminPool, "owner-b")
	firmB, err := wizard.CreateDefaultFirm(ctx, appPool, ownerB, "Firm B", wizard.SeedSelection{Sells: true, TracksInventory: true, ManagesCRM: true})
	if err != nil {
		t.Fatalf("CreateDefaultFirm B: %v", err)
	}

	// ownerB is a real owner of firm B, but not a member of firm A at all -
	// supplying firm A's genuine ID must not distinguish "real firm I'm not
	// in" from "unknown firm".
	if _, err := auditlog.List(ctx, appPool, firmA.FirmID, ownerB, auditlog.ListOptions{}); !errors.Is(err, auditlog.ErrFirmNotFound) {
		t.Fatalf("expected ErrFirmNotFound, got: %v", err)
	}

	// Sanity: ownerB can read firm B's own log.
	if _, err := auditlog.List(ctx, appPool, firmB.FirmID, ownerB, auditlog.ListOptions{}); err != nil {
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
	firm, err := wizard.CreateDefaultFirm(ctx, appPool, owner, "Firm C", wizard.SeedSelection{Sells: true, TracksInventory: true, ManagesCRM: true})
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	salesUserID := seedRoleWithoutAuditAccess(ctx, t, adminPool, firm.FirmID, "sales-c")

	if _, err := auditlog.List(ctx, appPool, firm.FirmID, salesUserID, auditlog.ListOptions{}); !errors.Is(err, auditlog.ErrPermissionDenied) {
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
	firm, err := wizard.CreateDefaultFirm(ctx, appPool, userID, "Firm E", wizard.SeedSelection{Sells: true, TracksInventory: true, ManagesCRM: true})
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

// secondDefinitionSpec is a second, structurally different workflow
// definition (mirrors internal/workflow's own purchaseOrderSpec test
// fixture) - used below purely so a firm has two distinct
// workflow_definitions to prove the ?definitionId= filter actually
// distinguishes between them, not just "narrows to workflow entries in
// general".
var secondDefinitionSpec = workflow.DefinitionSpec{
	Key:  "purchase_order",
	Name: "Purchase Order",
	CreatePermission: workflow.PermissionSpec{
		Key:         "purchase_order.create",
		Description: "Create a purchase order",
	},
	States: []workflow.StateSpec{
		{Key: "draft", Name: "Draft", IsInitial: true},
		{Key: "approved", Name: "Approved", IsTerminal: true},
	},
	Transitions: []workflow.TransitionSpec{
		{
			FromStateKey: "draft",
			ToStateKey:   "approved",
			ActionKey:    "approve",
			Name:         "Approve",
			Permission: workflow.PermissionSpec{
				Key:         "purchase_order.approve",
				Description: "Approve a purchase order",
			},
		},
	},
}

// seedAuditLogEntries inserts count directly-crafted audit_log rows for
// firmID/userID, one second apart starting at start, most recent last -
// used by the pagination/filtering tests below, which need deterministic
// occurred_at ordering rather than whatever timing CreateInstance calls
// would produce.
func seedAuditLogEntries(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID, userID uuid.UUID, start time.Time, count int) []time.Time {
	t.Helper()
	// Postgres's timestamptz column only carries microsecond precision;
	// Truncate here so the times this function returns (later reused
	// directly as From/To filter bounds by callers) exactly match what
	// comes back out of the database, rather than differing by whatever
	// sub-microsecond remainder time.Now() happened to carry.
	start = start.Truncate(time.Microsecond)
	occurredTimes := make([]time.Time, count)
	for i := 0; i < count; i++ {
		occurredAt := start.Add(time.Duration(i) * time.Second)
		occurredTimes[i] = occurredAt
		if _, err := adminPool.Exec(ctx, `
			INSERT INTO audit_log (firm_id, user_id, entity_type, entity_id, action, changes, occurred_at)
			VALUES ($1, $2, 'test_entity', gen_random_uuid(), 'test', '{}'::jsonb, $3)
		`, firmID, userID, occurredAt); err != nil {
			t.Fatalf("seed audit_log row %d: %v", i, err)
		}
	}
	return occurredTimes
}

// TestList_PaginatesInOccurredAtDescOrderWithCorrectBoundaries seeds a
// firm with several audit_log entries spanning multiple pages and proves
// Total reflects the full unpaged count, page boundaries don't overlap or
// skip rows, and ordering stays most-recent-first across pages - the
// same "prove page boundaries are correct" bar
// internal/workflow's TestListInstances pagination tests set.
func TestList_PaginatesInOccurredAtDescOrderWithCorrectBoundaries(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	owner := seedUser(ctx, t, adminPool, "owner-page")
	firm, err := wizard.CreateDefaultFirm(ctx, appPool, owner, "Firm Page", wizard.SeedSelection{Sells: true, TracksInventory: true, ManagesCRM: true})
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}
	// CreateDefaultFirm itself writes one "firm create" entry; seed 5 more
	// deterministically-timed entries on top of it, for 6 total.
	base := time.Now().Add(-time.Hour)
	seedAuditLogEntries(ctx, t, adminPool, firm.FirmID, owner, base, 5)

	const total = 6
	const pageSize = 2

	var seenIDs []uuid.UUID
	for offset := 0; offset < total; offset += pageSize {
		page, err := auditlog.List(ctx, appPool, firm.FirmID, owner, auditlog.ListOptions{Limit: pageSize, Offset: offset})
		if err != nil {
			t.Fatalf("List page at offset %d: %v", offset, err)
		}
		if page.Total != total {
			t.Errorf("offset %d: expected Total %d, got %d", offset, total, page.Total)
		}
		if len(page.Entries) != pageSize {
			t.Errorf("offset %d: expected %d entries, got %d", offset, pageSize, len(page.Entries))
		}
		for i := 1; i < len(page.Entries); i++ {
			if page.Entries[i].OccurredAt.After(page.Entries[i-1].OccurredAt) {
				t.Errorf("offset %d: entries not in descending occurred_at order", offset)
			}
		}
		for _, e := range page.Entries {
			seenIDs = append(seenIDs, e.ID)
		}
	}

	// Every page's rows together must equal exactly the full unpaged set,
	// with no duplicate (overlapping boundary) or missing (skipped
	// boundary) row.
	full, err := auditlog.List(ctx, appPool, firm.FirmID, owner, auditlog.ListOptions{})
	if err != nil {
		t.Fatalf("List full: %v", err)
	}
	if len(full.Entries) != total {
		t.Fatalf("expected %d entries unpaged, got %d", total, len(full.Entries))
	}
	seen := make(map[uuid.UUID]bool, len(seenIDs))
	for _, id := range seenIDs {
		if seen[id] {
			t.Errorf("entry %v appeared on more than one page", id)
		}
		seen[id] = true
	}
	if len(seen) != total {
		t.Errorf("expected %d distinct entries across all pages, got %d", total, len(seen))
	}
}

// TestList_FiltersByDateRange proves ?from=/?to= actually narrows the
// result set to the requested window, not just accepts the params.
func TestList_FiltersByDateRange(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	owner := seedUser(ctx, t, adminPool, "owner-daterange")
	firm, err := wizard.CreateDefaultFirm(ctx, appPool, owner, "Firm DateRange", wizard.SeedSelection{Sells: true, TracksInventory: true, ManagesCRM: true})
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	base := time.Now().Add(-24 * time.Hour)
	occurredTimes := seedAuditLogEntries(ctx, t, adminPool, firm.FirmID, owner, base, 5)
	// occurredTimes[0..4] are base, base+1s, base+2s, base+3s, base+4s.

	from := occurredTimes[1]
	to := occurredTimes[3]
	result, err := auditlog.List(ctx, appPool, firm.FirmID, owner, auditlog.ListOptions{From: &from, To: &to})
	if err != nil {
		t.Fatalf("List with date range: %v", err)
	}
	// Expect exactly the 3 seeded rows at indices 1..3 (inclusive) - the
	// firm-creation entry and the two seeded rows outside [from, to] must
	// be excluded.
	if result.Total != 3 {
		t.Fatalf("expected 3 entries within [from, to], got %d (Total=%d)", len(result.Entries), result.Total)
	}
	for _, e := range result.Entries {
		if e.OccurredAt.Before(from) || e.OccurredAt.After(to) {
			t.Errorf("entry occurred_at %v outside requested range [%v, %v]", e.OccurredAt, from, to)
		}
	}

	// Sanity: no filter returns more (at least the firm-creation entry
	// plus all 5 seeded rows).
	unfiltered, err := auditlog.List(ctx, appPool, firm.FirmID, owner, auditlog.ListOptions{})
	if err != nil {
		t.Fatalf("List unfiltered: %v", err)
	}
	if unfiltered.Total <= result.Total {
		t.Errorf("expected the date-range filter to narrow results: unfiltered=%d, filtered=%d", unfiltered.Total, result.Total)
	}
}

// TestList_FiltersByWorkflowDefinition proves ?definitionId= narrows
// results to entries that resolve back to that specific workflow
// definition (via workflow_instances.workflow_definition_id, see List's
// doc comment) - not to "any workflow entry", and not to the other
// definition's own entries.
func TestList_FiltersByWorkflowDefinition(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	owner := seedUser(ctx, t, adminPool, "owner-defn-filter")
	firm, err := wizard.CreateDefaultFirm(ctx, appPool, owner, "Firm DefnFilter", wizard.SeedSelection{Sells: true, TracksInventory: true, ManagesCRM: true})
	if err != nil {
		t.Fatalf("CreateDefaultFirm: %v", err)
	}

	poDefID, err := workflow.DefineWorkflowForFirm(ctx, appPool, firm.FirmID, owner, secondDefinitionSpec)
	if err != nil {
		t.Fatalf("DefineWorkflowForFirm: %v", err)
	}

	stockInstanceID, err := workflow.CreateInstance(ctx, appPool, firm.FirmID, owner, firm.StockToSaleDefinitionID, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance (stock): %v", err)
	}
	poInstanceID, err := workflow.CreateInstance(ctx, appPool, firm.FirmID, owner, poDefID, map[string]any{"vendor": "Acme Supplies"})
	if err != nil {
		t.Fatalf("CreateInstance (purchase order): %v", err)
	}

	filtered, err := auditlog.List(ctx, appPool, firm.FirmID, owner, auditlog.ListOptions{DefinitionID: &poDefID})
	if err != nil {
		t.Fatalf("List filtered by definition: %v", err)
	}
	if len(filtered.Entries) == 0 {
		t.Fatal("expected at least one entry for the purchase-order definition")
	}
	for _, e := range filtered.Entries {
		if e.EntityType != "workflow_instance" || e.EntityID != poInstanceID {
			t.Errorf("expected only the purchase-order instance's entries, got entity_type=%q entity_id=%v", e.EntityType, e.EntityID)
		}
	}

	// The stock instance's own creation entry must never appear when
	// filtering by the purchase-order definition.
	for _, e := range filtered.Entries {
		if e.EntityID == stockInstanceID {
			t.Errorf("stock instance entry %v leaked into the purchase-order definition filter", stockInstanceID)
		}
	}

	unfiltered, err := auditlog.List(ctx, appPool, firm.FirmID, owner, auditlog.ListOptions{})
	if err != nil {
		t.Fatalf("List unfiltered: %v", err)
	}
	if unfiltered.Total <= filtered.Total {
		t.Errorf("expected the definition filter to narrow results: unfiltered=%d, filtered=%d", unfiltered.Total, filtered.Total)
	}
}
