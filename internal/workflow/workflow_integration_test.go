// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// These tests exercise the workflow engine against a real Postgres
// instance - RLS isolation and permission enforcement can't be
// meaningfully faked, same convention as internal/platform/db's and
// internal/identity's integration tests. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run workflow integration tests")
	}
	return adminDSN, appDSN
}

// seedUserInFirm creates a user (a global table) and, within firmID
// (already created by the caller), a role granted exactly
// grantedPermissions and the user's membership in that role - all through
// WithFirmContext, exactly as application code would touch these
// tenant-scoped tables. Permission keys referenced here must already exist
// in the global catalog (i.e. call workflow.SeedStockToSaleWorkflow, which
// upserts them, before this).
func seedUserInFirm(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmID uuid.UUID, keycloakSubject string, grantedPermissions ...string) (userID, roleID uuid.UUID) {
	t.Helper()

	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test User").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (firm_id, key, name) VALUES ($1, 'tester', 'Tester') RETURNING id
		`, firmID).Scan(&roleID); err != nil {
			return err
		}
		for _, key := range grantedPermissions {
			if _, err := tx.Exec(ctx, `
				INSERT INTO role_permissions (firm_id, role_id, permission_key) VALUES ($1, $2, $3)
			`, firmID, roleID, key); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)
		`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed role/grants/membership: %v", err)
	}

	return userID, roleID
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
			workflow_instances, audit_log, permissions, customers CASCADE
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

func TestDefineWorkflow_SeedsStatesTransitionsAndPermissions(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}

	var stateCount, transitionCount int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM workflow_states WHERE workflow_definition_id = $1`, definitionID).Scan(&stateCount); err != nil {
		t.Fatalf("count states: %v", err)
	}
	if stateCount != 2 {
		t.Errorf("expected 2 states, got %d", stateCount)
	}
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM workflow_transitions WHERE workflow_definition_id = $1`, definitionID).Scan(&transitionCount); err != nil {
		t.Fatalf("count transitions: %v", err)
	}
	if transitionCount != 1 {
		t.Errorf("expected 1 transition, got %d", transitionCount)
	}

	for _, key := range []string{workflow.AddStockPermission, workflow.RecordSalePermission} {
		var exists bool
		if err := adminPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM permissions WHERE key = $1)`, key).Scan(&exists); err != nil {
			t.Fatalf("check permission %q: %v", key, err)
		}
		if !exists {
			t.Errorf("expected permission %q to be upserted into the global catalog, but it's missing", key)
		}
	}

	// Defining the same workflow again for a second firm must not fail on
	// account of the permission keys already existing globally (upsert,
	// not insert-or-die) - the catalog is shared, grants are per-firm.
	var firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmB); err != nil {
		t.Fatalf("SeedStockToSaleWorkflow for a second firm should succeed via permission upsert, got: %v", err)
	}
}

// TestDefineWorkflowTx_GrantsPermissionsToGivenRole covers the
// self-action auto-grant: a caller that supplies a real granteeRoleID to
// DefineWorkflowTx (as internal/wizard.CreateDefaultFirm does for its
// default "owner" role) should see that role granted every permission
// the spec introduces, in the same transaction - not as a separate step.
func TestDefineWorkflowTx_GrantsPermissionsToGivenRole(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	var roleID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO roles (firm_id, key, name) VALUES ($1, 'owner', 'Owner') RETURNING id
	`, firmID).Scan(&roleID); err != nil {
		t.Fatalf("seed role: %v", err)
	}

	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := workflow.DefineWorkflowTx(ctx, tx, firmID, roleID, workflow.StockToSaleSpec)
		return err
	})
	if err != nil {
		t.Fatalf("DefineWorkflowTx: %v", err)
	}

	for _, key := range workflow.StockToSaleSpec.PermissionKeys() {
		var granted bool
		if err := adminPool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM role_permissions WHERE firm_id = $1 AND role_id = $2 AND permission_key = $3)
		`, firmID, roleID, key).Scan(&granted); err != nil {
			t.Fatalf("check role_permissions for %q: %v", key, err)
		}
		if !granted {
			t.Errorf("expected role %v to be granted permission %q", roleID, key)
		}
	}
}

// TestDefineWorkflow_DoesNotGrantAnyRole covers the other half: the
// pool-based DefineWorkflow/SeedStockToSaleWorkflow entry points (used
// only by fixtures/tests, per their own doc comments) must not silently
// start granting permissions to any role - TestCreateInstance_
// RequiresPermission below already depends on this (it deliberately seeds
// a role with nothing granted), but this test asserts it directly:
// nothing in role_permissions should exist for a firm right after
// SeedStockToSaleWorkflow, before any role has been seeded at all.
func TestDefineWorkflow_DoesNotGrantAnyRole(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("SeedStockToSaleWorkflow: %v", err)
	}

	var count int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM role_permissions WHERE firm_id = $1`, firmID).Scan(&count); err != nil {
		t.Fatalf("count role_permissions: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no role_permissions rows from a pool-based SeedStockToSaleWorkflow call, got %d", count)
	}
}

func TestCreateInstance_RequiresPermission(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	// Deliberately grant nothing.
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-no-perms")

	_, err = workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget"})
	if !errors.Is(err, workflow.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got: %v", err)
	}
}

func TestCreateInstance_SucceedsAndAudits(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-add-stock", workflow.AddStockPermission)

	before := time.Now().Add(-time.Second)
	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget", "quantity": float64(10)})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.State.Key != "in_stock" {
		t.Errorf("expected new instance in state 'in_stock', got %q", state.State.Key)
	}
	if state.Payload["item"] != "widget" {
		t.Errorf("expected payload to include the creation payload, got %v", state.Payload)
	}
	if len(state.AvailableActions) != 1 || state.AvailableActions[0].ActionKey != "record_sale" {
		t.Errorf("expected exactly one available action 'record_sale', got %+v", state.AvailableActions)
	}

	// Audit: who, when, what changed.
	var auditUserID uuid.UUID
	var entityType, action string
	var changesJSON []byte
	var occurredAt time.Time
	err = adminPool.QueryRow(ctx, `
		SELECT user_id, entity_type, action, changes, occurred_at
		FROM audit_log WHERE entity_id = $1
	`, instanceID).Scan(&auditUserID, &entityType, &action, &changesJSON, &occurredAt)
	if err != nil {
		t.Fatalf("query audit log: %v", err)
	}
	if auditUserID != userID {
		t.Errorf("expected audit log user_id %s, got %s", userID, auditUserID)
	}
	if entityType != "workflow_instance" {
		t.Errorf("expected entity_type 'workflow_instance', got %q", entityType)
	}
	if action != "create" {
		t.Errorf("expected action 'create', got %q", action)
	}
	if occurredAt.Before(before) {
		t.Errorf("expected occurred_at to be recent, got %v (before %v)", occurredAt, before)
	}
	var changes map[string]any
	if err := json.Unmarshal(changesJSON, &changes); err != nil {
		t.Fatalf("unmarshal changes: %v", err)
	}
	if changes["to_state"] != "in_stock" {
		t.Errorf("expected audit changes to record to_state 'in_stock', got %v", changes)
	}
}

func TestExecuteTransition_RequiresPermission(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	// Granted add_stock but deliberately not record_sale.
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-add-only", workflow.AddStockPermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	err = workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", map[string]any{"price": float64(9.99)})
	if !errors.Is(err, workflow.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got: %v", err)
	}

	// The instance must not have moved - a denied permission check must
	// not leave a partial mutation.
	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.State.Key != "in_stock" {
		t.Errorf("expected instance to remain in 'in_stock' after a denied transition, got %q", state.State.Key)
	}
}

// TestExecuteTransition_NonMemberGetsNotFoundNotPermissionDenied is the
// Open Points item 37 audit's regression test for ExecuteTransition: a
// caller who isn't a member of firmID at all, but supplies a real
// firmID/instanceID pair belonging to someone else's firm, must not be
// able to observe the instance's existence, current state, or which
// transitions structurally exist from it - it must look exactly like an
// unknown instance, not a permission denial (which would confirm the
// instance is real).
func TestExecuteTransition_NonMemberGetsNotFoundNotPermissionDenied(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	definitionA, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmA)
	if err != nil {
		t.Fatalf("seed workflow A: %v", err)
	}
	userA, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmA, "sub-firm-a-exec", workflow.AddStockPermission)
	instanceID, err := workflow.CreateInstance(ctx, appPool, firmA, userA, definitionA, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	var firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	userB, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmB, "sub-outsider-exec")

	err = workflow.ExecuteTransition(ctx, appPool, firmA, userB, instanceID, "record_sale", map[string]any{})
	if !errors.Is(err, workflow.ErrInstanceNotFound) {
		t.Fatalf("expected ErrInstanceNotFound for a non-member supplying a real instance ID, got: %v", err)
	}
}

func TestExecuteTransition_SucceedsAndAudits(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-full", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", map[string]any{"price": float64(9.99)}); err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}

	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.State.Key != "sold" {
		t.Errorf("expected instance to be in 'sold' after record_sale, got %q", state.State.Key)
	}
	if len(state.AvailableActions) != 0 {
		t.Errorf("expected no further actions from terminal state 'sold', got %+v", state.AvailableActions)
	}
	if state.Payload["item"] != "widget" || state.Payload["price"] != 9.99 {
		t.Errorf("expected payload to be the shallow merge of creation + transition payloads, got %v", state.Payload)
	}

	var auditCount int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE entity_id = $1`, instanceID).Scan(&auditCount); err != nil {
		t.Fatalf("count audit log rows: %v", err)
	}
	if auditCount != 2 {
		t.Errorf("expected 2 audit log entries (create + record_sale), got %d", auditCount)
	}

	var transitionAction string
	if err := adminPool.QueryRow(ctx, `
		SELECT action FROM audit_log WHERE entity_id = $1 AND action = 'record_sale'
	`, instanceID).Scan(&transitionAction); err != nil {
		t.Fatalf("expected an audit_log row with action 'record_sale': %v", err)
	}

	// Attempting the same transition again from the now-terminal 'sold'
	// state must fail: there is no such transition from there.
	err = workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", map[string]any{})
	if !errors.Is(err, workflow.ErrNoSuchTransition) {
		t.Fatalf("expected ErrNoSuchTransition from the terminal state, got: %v", err)
	}
}

// TestExecuteTransition_NilPayloadDoesNotCorruptStoredPayload is a
// regression test for a real bug the CI pipeline's E2E smoke test (CI
// Checklist item 12, scripts/e2e_smoke_test.sh) caught: a nil payload map
// marshals to the JSON literal `null`, and `payload || $2::jsonb`'s
// merge treats a jsonb null operand as a scalar to concatenate rather
// than a no-op - Postgres turns `{"item":"widget"} || null` into
// `[{"item":"widget"}, null]`, an array, silently corrupting the stored
// payload and breaking every later read of it (CurrentState's
// json.Unmarshal into map[string]any fails outright once that happens).
// A nil payload is exactly what happens whenever a caller's request body
// omits the "payload" field entirely - decodeJSONBody's own doc comment
// promises "a missing/empty body is treated as no payload given," so this
// must be safe, not just the common case where a caller happens to send
// an explicit `{}`.
func TestExecuteTransition_NilPayloadDoesNotCorruptStoredPayload(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-nil-payload", workflow.AddStockPermission, workflow.RecordSalePermission)

	// CreateInstance with a nil payload (the "add stock" API's own
	// decodeJSONBody produces exactly this for an omitted "payload" field).
	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, nil)
	if err != nil {
		t.Fatalf("CreateInstance with nil payload: %v", err)
	}

	// ExecuteTransition with a nil payload too - this is the exact call
	// shape that corrupted the stored payload before the fix.
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, instanceID, "record_sale", nil); err != nil {
		t.Fatalf("ExecuteTransition with nil payload: %v", err)
	}

	state, err := workflow.CurrentState(ctx, appPool, firmID, userID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v (a corrupted payload makes even reading the instance fail)", err)
	}
	if state.State.Key != "sold" {
		t.Errorf("expected instance to be in 'sold', got %q", state.State.Key)
	}

	var rawPayload string
	if err := adminPool.QueryRow(ctx, `SELECT payload::text FROM workflow_instances WHERE id = $1`, instanceID).Scan(&rawPayload); err != nil {
		t.Fatalf("read raw payload: %v", err)
	}
	if rawPayload != "{}" {
		t.Errorf("expected the stored payload to remain a JSON object (\"{}\"), got %q - the payload was corrupted into something else", rawPayload)
	}
}

func TestRLS_InstanceIsNotVisibleFromAnotherFirm(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	definitionA, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmA)
	if err != nil {
		t.Fatalf("seed workflow A: %v", err)
	}
	userA, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmA, "sub-firm-a", workflow.AddStockPermission, workflow.RecordSalePermission)

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmA, userA, definitionA, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	var firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	// Firm B never touches firmA's instance - it doesn't even need its own
	// workflow defined to prove the isolation.
	userB, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmB, "sub-firm-b", workflow.AddStockPermission, workflow.RecordSalePermission)

	if _, err := workflow.CurrentState(ctx, appPool, firmB, userB, instanceID); !errors.Is(err, workflow.ErrInstanceNotFound) {
		t.Fatalf("expected ErrInstanceNotFound reading Firm A's instance under Firm B's context, got: %v", err)
	}

	// The other half of the same isolation guarantee, from the opposite
	// direction: userB (a real member of firmB, nothing to do with firmA)
	// must not be able to read firmA's own instance just by passing
	// firmA's real ID as the firmID argument. Before permission.IsMember
	// was added to CurrentState, WithFirmContext's RLS scoping alone was
	// satisfied by firmA's ID being genuine, regardless of whether the
	// caller belonged to it - this is the regression test for that.
	if _, err := workflow.CurrentState(ctx, appPool, firmA, userB, instanceID); !errors.Is(err, workflow.ErrInstanceNotFound) {
		t.Fatalf("expected ErrInstanceNotFound when a non-member of firm A reads firm A's own instance by ID, got: %v", err)
	}

	err = workflow.ExecuteTransition(ctx, appPool, firmB, userB, instanceID, "record_sale", map[string]any{})
	if !errors.Is(err, workflow.ErrInstanceNotFound) {
		t.Fatalf("expected ErrInstanceNotFound executing a transition on Firm A's instance under Firm B's context, got: %v", err)
	}
}

func TestCreateInstance_UnknownDefinition(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-x")

	_, err := workflow.CreateInstance(ctx, appPool, firmID, userID, uuid.New(), map[string]any{})
	if !errors.Is(err, workflow.ErrDefinitionNotFound) {
		t.Fatalf("expected ErrDefinitionNotFound, got: %v", err)
	}
}

// TestCreateInstance_NonMemberGetsNotFoundNotPermissionDenied is the
// Open Points item 37 audit's regression test for CreateInstance: a
// caller who isn't a member of firmID at all, but supplies a real
// firmID/definitionID pair belonging to someone else's firm, must not be
// able to observe that the definition exists (e.g. via a
// permission-denied response that implies the create_permission_key
// lookup succeeded) - it must look exactly like an unknown definition.
func TestCreateInstance_NonMemberGetsNotFoundNotPermissionDenied(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	definitionA, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmA)
	if err != nil {
		t.Fatalf("seed workflow A: %v", err)
	}

	var firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	userB, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmB, "sub-outsider-create")

	if _, err := workflow.CreateInstance(ctx, appPool, firmA, userB, definitionA, map[string]any{"item": "widget"}); !errors.Is(err, workflow.ErrDefinitionNotFound) {
		t.Fatalf("expected ErrDefinitionNotFound for a non-member supplying a real definition ID, got: %v", err)
	}
}

func TestLookupDefinitionByKey_ResolvesToTheSeededDefinition(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-lookup")

	info, err := workflow.LookupDefinitionByKey(ctx, appPool, firmID, userID, workflow.StockToSaleKey)
	if err != nil {
		t.Fatalf("LookupDefinitionByKey: %v", err)
	}
	if info.ID != definitionID {
		t.Errorf("expected definition ID %v, got %v", definitionID, info.ID)
	}
	if info.Key != workflow.StockToSaleKey {
		t.Errorf("expected key %q, got %q", workflow.StockToSaleKey, info.Key)
	}
	if info.CreatePermissionKey != workflow.AddStockPermission {
		t.Errorf("expected create permission key %q, got %q", workflow.AddStockPermission, info.CreatePermissionKey)
	}
}

func TestLookupDefinitionByKey_UnknownKey(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-unknown-key")

	if _, err := workflow.LookupDefinitionByKey(ctx, appPool, firmID, userID, "no_such_workflow"); !errors.Is(err, workflow.ErrDefinitionNotFound) {
		t.Fatalf("expected ErrDefinitionNotFound, got: %v", err)
	}
}

// TestLookupDefinitionByKey_RLS covers the same key existing for two
// different firms (each firm gets its own workflow_definitions row per
// migrations/0003_workflow_engine.up.sql's UNIQUE (firm_id, key)) -
// firm B's lookup must resolve to its own definition, never firm A's -
// and, separately, that a user who isn't a member of firm A at all can't
// resolve firm A's definition just by supplying firm A's real ID (the
// membership-bypass this function's permission.IsMember check exists to
// close; see CurrentState's doc comment and
// TestRLS_InstanceIsNotVisibleFromAnotherFirm for the same regression
// covered on the instance-reading side).
func TestLookupDefinitionByKey_RLS(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	definitionA, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmA)
	if err != nil {
		t.Fatalf("seed workflow A: %v", err)
	}

	var firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	definitionB, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmB)
	if err != nil {
		t.Fatalf("seed workflow B: %v", err)
	}
	if definitionA == definitionB {
		t.Fatal("expected each firm to get its own workflow_definitions row")
	}
	userB, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmB, "sub-lookup-b")

	infoB, err := workflow.LookupDefinitionByKey(ctx, appPool, firmB, userB, workflow.StockToSaleKey)
	if err != nil {
		t.Fatalf("LookupDefinitionByKey under firm B: %v", err)
	}
	if infoB.ID != definitionB {
		t.Errorf("expected firm B's lookup to resolve to firm B's definition %v, got %v", definitionB, infoB.ID)
	}

	if _, err := workflow.LookupDefinitionByKey(ctx, appPool, firmA, userB, workflow.StockToSaleKey); !errors.Is(err, workflow.ErrDefinitionNotFound) {
		t.Fatalf("expected ErrDefinitionNotFound when a non-member of firm A looks it up by firm A's real ID, got: %v", err)
	}
}

// purchaseOrderSpec is a second, structurally different workflow
// definition - three states/two transitions vs. Stock In -> Sale's two
// states/one transition - used only by TestListDefinitions below to
// prove ListDefinitions (and, transitively, the frontend it feeds) is
// genuinely generic across workflow shapes, not just happening to work
// for the one shipped workflow. Not seeded for real firms anywhere -
// test-fixture only, same as this file's other synthetic firms/roles.
var purchaseOrderSpec = workflow.DefinitionSpec{
	Key:  "purchase_order",
	Name: "Purchase Order",
	CreatePermission: workflow.PermissionSpec{
		Key:         "workflow.purchase_order.create",
		Description: "Create a new purchase order.",
	},
	States: []workflow.StateSpec{
		{Key: "draft", Name: "Draft", IsInitial: true},
		{Key: "approved", Name: "Approved"},
		{Key: "received", Name: "Received", IsTerminal: true},
	},
	Transitions: []workflow.TransitionSpec{
		{
			FromStateKey: "draft",
			ToStateKey:   "approved",
			ActionKey:    "approve",
			Name:         "Approve",
			Permission: workflow.PermissionSpec{
				Key:         "workflow.purchase_order.approve",
				Description: "Approve a draft purchase order.",
			},
		},
		{
			FromStateKey: "approved",
			ToStateKey:   "received",
			ActionKey:    "receive",
			Name:         "Receive",
			Permission: workflow.PermissionSpec{
				Key:         "workflow.purchase_order.receive",
				Description: "Mark an approved purchase order as received.",
			},
		},
	},
}

func TestListDefinitions_ReturnsEveryDefinitionForTheFirm(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	stockDefID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed stock_to_sale workflow: %v", err)
	}
	poDefID, err := workflow.DefineWorkflow(ctx, appPool, firmID, purchaseOrderSpec)
	if err != nil {
		t.Fatalf("define purchase_order workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-list-defs")

	defs, err := workflow.ListDefinitions(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("ListDefinitions: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d: %+v", len(defs), defs)
	}

	byID := make(map[uuid.UUID]workflow.DefinitionInfo, len(defs))
	for _, d := range defs {
		byID[d.ID] = d
	}
	stock, ok := byID[stockDefID]
	if !ok {
		t.Fatalf("expected stock_to_sale definition %v in the list: %+v", stockDefID, defs)
	}
	if stock.Key != workflow.StockToSaleKey || stock.CreatePermissionKey != workflow.AddStockPermission {
		t.Errorf("unexpected stock_to_sale entry: %+v", stock)
	}
	po, ok := byID[poDefID]
	if !ok {
		t.Fatalf("expected purchase_order definition %v in the list: %+v", poDefID, defs)
	}
	if po.Key != "purchase_order" || po.CreatePermissionKey != "workflow.purchase_order.create" {
		t.Errorf("unexpected purchase_order entry: %+v", po)
	}
}

// TestListDefinitions_RLS mirrors TestLookupDefinitionByKey_RLS: a second
// firm's definitions must never appear in the first firm's list, and a
// non-member of a firm can't enumerate its definitions just by supplying
// that firm's real ID.
func TestListDefinitions_RLS(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA, firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmA); err != nil {
		t.Fatalf("seed workflow A: %v", err)
	}
	poDefIDB, err := workflow.DefineWorkflow(ctx, appPool, firmB, purchaseOrderSpec)
	if err != nil {
		t.Fatalf("define workflow B: %v", err)
	}
	userB, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmB, "sub-list-defs-b")

	defsB, err := workflow.ListDefinitions(ctx, appPool, firmB, userB)
	if err != nil {
		t.Fatalf("ListDefinitions under firm B: %v", err)
	}
	if len(defsB) != 1 || defsB[0].ID != poDefIDB {
		t.Fatalf("expected firm B's list to contain only its own definition %v, got: %+v", poDefIDB, defsB)
	}

	if _, err := workflow.ListDefinitions(ctx, appPool, firmA, userB); !errors.Is(err, workflow.ErrDefinitionNotFound) {
		t.Fatalf("expected ErrDefinitionNotFound when a non-member of firm A lists it by firm A's real ID, got: %v", err)
	}
}

// seedOwnerInFirm is seedUserInFirm's owner-flagged counterpart, for
// TestDefineWorkflowForFirm's tests below - DefineWorkflowForFirm is
// gated to is_owner (internal/permission.IsOwner), not an ordinary
// granted permission, so its own tests need a role that actually carries
// that flag rather than seedUserInFirm's plain "tester" role.
func seedOwnerInFirm(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmID uuid.UUID, keycloakSubject string) (userID, roleID uuid.UUID) {
	t.Helper()

	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test Owner").Scan(&userID); err != nil {
		t.Fatalf("seed owner user: %v", err)
	}

	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (firm_id, key, name, is_owner) VALUES ($1, 'owner', 'Owner', true) RETURNING id
		`, firmID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)
		`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed owner role/membership: %v", err)
	}

	return userID, roleID
}

// purchaseOrderRequestSpec mirrors purchaseOrderSpec (see
// TestListDefinitions_ReturnsEveryDefinitionForTheFirm above) but is used
// for TestDefineWorkflowForFirm - a firm's owner actually defining a new
// workflow through the mechanism this batch exposes over HTTP, not a Go
// fixture seeding one directly.
var purchaseOrderRequestSpec = purchaseOrderSpec

func TestDefineWorkflowForFirm_OwnerCanDefineANewWorkflow(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerID, ownerRoleID := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "sub-define-owner")

	definitionID, err := workflow.DefineWorkflowForFirm(ctx, appPool, firmID, ownerID, purchaseOrderRequestSpec)
	if err != nil {
		t.Fatalf("DefineWorkflowForFirm: %v", err)
	}
	if definitionID == uuid.Nil {
		t.Fatal("expected a real definition ID")
	}

	// Resolvable afterward, same as any other definition.
	info, err := workflow.LookupDefinitionByKey(ctx, appPool, firmID, ownerID, "purchase_order")
	if err != nil {
		t.Fatalf("LookupDefinitionByKey after define: %v", err)
	}
	if info.ID != definitionID {
		t.Errorf("expected %v, got %v", definitionID, info.ID)
	}

	// Self-action auto-grant: the acting owner's own role should now hold
	// every permission the new spec introduced, without a separate grant
	// step - CreateInstance should succeed for that same user immediately.
	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, ownerID, definitionID, map[string]any{"vendor": "Acme Supply"})
	if err != nil {
		t.Fatalf("expected the defining owner to already hold the create permission (self-action auto-grant), got: %v", err)
	}
	if instanceID == uuid.Nil {
		t.Fatal("expected a real instance ID")
	}

	var grantedCount int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM role_permissions WHERE role_id = $1 AND permission_key IN ($2, $3, $4)
	`, ownerRoleID, "workflow.purchase_order.create", "workflow.purchase_order.approve", "workflow.purchase_order.receive").Scan(&grantedCount); err != nil {
		t.Fatalf("query role_permissions: %v", err)
	}
	if grantedCount != 3 {
		t.Errorf("expected all 3 purchase_order permissions granted to the owner role, got %d", grantedCount)
	}

	// Audit trail completeness (Open Points item 41 batch's audit-trail
	// audit): defining a new workflow is a structural, owner-only mutation
	// and must leave an audit_log row, same as every other mutating path
	// in this package.
	var auditAction string
	if err := adminPool.QueryRow(ctx, `
		SELECT action FROM audit_log WHERE entity_id = $1 AND entity_type = 'workflow_definition'
	`, definitionID).Scan(&auditAction); err != nil {
		t.Fatalf("expected an audit_log row for the newly defined workflow: %v", err)
	}
	if auditAction != "create" {
		t.Errorf("expected audit_log action 'create', got %q", auditAction)
	}
}

func TestDefineWorkflowForFirm_NonOwnerIsDenied(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	// A plain member, not an owner - seedUserInFirm's "tester" role never
	// sets is_owner.
	memberID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-define-nonowner")

	if _, err := workflow.DefineWorkflowForFirm(ctx, appPool, firmID, memberID, purchaseOrderRequestSpec); !errors.Is(err, workflow.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied for a non-owner member, got: %v", err)
	}
}

func TestDefineWorkflowForFirm_NonMemberIsDenied(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA, firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	outsiderID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmB, "sub-define-outsider")

	if _, err := workflow.DefineWorkflowForFirm(ctx, appPool, firmA, outsiderID, purchaseOrderRequestSpec); !errors.Is(err, workflow.ErrDefinitionNotFound) {
		t.Fatalf("expected ErrDefinitionNotFound for an owner of a different firm supplying firm A's real ID, got: %v", err)
	}
}

func TestDefineWorkflowForFirm_MalformedSpecIsRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	ownerID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "sub-define-malformed")

	noInitialState := workflow.DefinitionSpec{
		Key:  "broken",
		Name: "Broken",
		CreatePermission: workflow.PermissionSpec{
			Key:         "workflow.broken.create",
			Description: "Create a broken instance.",
		},
		States: []workflow.StateSpec{
			{Key: "only_state", Name: "Only State"}, // IsInitial never set
		},
	}

	if _, err := workflow.DefineWorkflowForFirm(ctx, appPool, firmID, ownerID, noInitialState); !errors.Is(err, workflow.ErrInvalidSpec) {
		t.Fatalf("expected ErrInvalidSpec for a spec with no initial state, got: %v", err)
	}
}

func TestDefineWorkflowForFirm_DuplicateKeyIsRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	ownerID, _ := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "sub-define-duplicate")

	duplicateKeySpec := workflow.DefinitionSpec{
		Key:  workflow.StockToSaleKey, // already exists for this firm
		Name: "Stock In -> Sale (again)",
		CreatePermission: workflow.PermissionSpec{
			Key:         "workflow.stock_to_sale_2.add_stock",
			Description: "Add a new stock item.",
		},
		States: []workflow.StateSpec{
			{Key: "in_stock", Name: "In Stock", IsInitial: true},
		},
	}

	if _, err := workflow.DefineWorkflowForFirm(ctx, appPool, firmID, ownerID, duplicateKeySpec); !errors.Is(err, workflow.ErrDefinitionKeyExists) {
		t.Fatalf("expected ErrDefinitionKeyExists when redefining an already-used key, got: %v", err)
	}
}

func TestListInstances_ReturnsEachInstanceWithItsStateAndActions(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-list", workflow.AddStockPermission, workflow.RecordSalePermission)

	inStockID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "widget", "quantity": float64(5)})
	if err != nil {
		t.Fatalf("CreateInstance (in stock): %v", err)
	}
	soldID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": "gadget", "quantity": float64(2)})
	if err != nil {
		t.Fatalf("CreateInstance (to be sold): %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, soldID, "record_sale", map[string]any{}); err != nil {
		t.Fatalf("ExecuteTransition: %v", err)
	}

	listResult, err := workflow.ListInstances(ctx, appPool, firmID, userID, definitionID, workflow.ListInstancesOptions{})
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	instances := listResult.Instances
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}
	if listResult.Total != 2 {
		t.Errorf("expected Total to be 2, got %d", listResult.Total)
	}

	byID := make(map[uuid.UUID]workflow.InstanceState, len(instances))
	for _, inst := range instances {
		byID[inst.InstanceID] = inst
	}

	inStock, ok := byID[inStockID]
	if !ok {
		t.Fatal("expected the in-stock instance to be listed")
	}
	if inStock.State.Key != "in_stock" {
		t.Errorf("expected in-stock instance to be in state 'in_stock', got %q", inStock.State.Key)
	}
	if inStock.Payload["item"] != "widget" || inStock.Payload["quantity"] != float64(5) {
		t.Errorf("expected in-stock instance payload to round-trip item/quantity, got %v", inStock.Payload)
	}
	if len(inStock.AvailableActions) != 1 || inStock.AvailableActions[0].ActionKey != "record_sale" {
		t.Errorf("expected the in-stock instance to offer exactly 'record_sale', got %+v", inStock.AvailableActions)
	}
	if inStock.AvailableActions[0].PermissionKey != workflow.RecordSalePermission {
		t.Errorf("expected the available action's permission key to be %q, got %q", workflow.RecordSalePermission, inStock.AvailableActions[0].PermissionKey)
	}

	sold, ok := byID[soldID]
	if !ok {
		t.Fatal("expected the sold instance to be listed")
	}
	if sold.State.Key != "sold" {
		t.Errorf("expected sold instance to be in state 'sold', got %q", sold.State.Key)
	}
	if len(sold.AvailableActions) != 0 {
		t.Errorf("expected no available actions from the terminal 'sold' state, got %+v", sold.AvailableActions)
	}
}

// TestListInstances_RLS covers item 1's requirement directly: a second
// firm's stock must never be visible through ListInstances, the function
// the stock list page's backend endpoint calls.
func TestListInstances_RLS(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	definitionA, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmA)
	if err != nil {
		t.Fatalf("seed workflow A: %v", err)
	}
	userA, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmA, "sub-firm-a-stock", workflow.AddStockPermission)
	if _, err := workflow.CreateInstance(ctx, appPool, firmA, userA, definitionA, map[string]any{"item": "firm-a-widget"}); err != nil {
		t.Fatalf("CreateInstance for firm A: %v", err)
	}

	var firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	definitionB, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmB)
	if err != nil {
		t.Fatalf("seed workflow B: %v", err)
	}
	userB, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmB, "sub-firm-b-stock", workflow.AddStockPermission)

	// Firm B has its own (empty) workflow definition but never touches
	// firm A's stock - listing under firm B's context must come back
	// empty, not leak firm A's instance.
	resultB, err := workflow.ListInstances(ctx, appPool, firmB, userB, definitionB, workflow.ListInstancesOptions{})
	if err != nil {
		t.Fatalf("ListInstances under firm B: %v", err)
	}
	if len(resultB.Instances) != 0 {
		t.Errorf("expected firm B to see no instances, got %d", len(resultB.Instances))
	}

	// Listing firm A's own definition ID under firm B's firm context
	// (RLS scoping, not the definition ID) must also come back empty -
	// the definition row itself isn't visible under the wrong firm
	// context, so its instances aren't either.
	resultCrossFirm, err := workflow.ListInstances(ctx, appPool, firmB, userB, definitionA, workflow.ListInstancesOptions{})
	if err != nil {
		t.Fatalf("ListInstances with firm A's definition under firm B's context: %v", err)
	}
	if len(resultCrossFirm.Instances) != 0 {
		t.Errorf("expected no instances when listing firm A's definition under firm B's firm context, got %d", len(resultCrossFirm.Instances))
	}

	// The membership-bypass regression: userB is not a member of firm A
	// at all, but supplies firm A's real ID (and firm A's real
	// definition ID) directly - this must fail outright, not succeed
	// (even with an empty result) just because both IDs happen to be
	// genuine. Before permission.IsMember was added to ListInstances,
	// this would have returned firm A's actual stock.
	if _, err := workflow.ListInstances(ctx, appPool, firmA, userB, definitionA, workflow.ListInstancesOptions{}); !errors.Is(err, workflow.ErrDefinitionNotFound) {
		t.Fatalf("expected ErrDefinitionNotFound when a non-member of firm A lists its instances by real ID, got: %v", err)
	}

	// Listing under the correct firm context still finds it.
	resultA, err := workflow.ListInstances(ctx, appPool, firmA, userA, definitionA, workflow.ListInstancesOptions{})
	if err != nil {
		t.Fatalf("ListInstances under firm A: %v", err)
	}
	if len(resultA.Instances) != 1 {
		t.Errorf("expected firm A to see exactly its 1 instance, got %d", len(resultA.Instances))
	}
}

// TestListInstances_PaginationAndSearch covers item 2: Limit/Offset page
// through results in creation order, and Search filters by payload/state
// content - generically, not any one definition's specific field names.
func TestListInstances_PaginationAndSearch(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm Paging') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-paging", workflow.AddStockPermission)

	items := []string{"widget", "gadget", "gizmo"}
	var ids []uuid.UUID
	for _, item := range items {
		id, err := workflow.CreateInstance(ctx, appPool, firmID, userID, definitionID, map[string]any{"item": item})
		if err != nil {
			t.Fatalf("CreateInstance(%q): %v", item, err)
		}
		ids = append(ids, id)
	}

	page1, err := workflow.ListInstances(ctx, appPool, firmID, userID, definitionID, workflow.ListInstancesOptions{Limit: 2})
	if err != nil {
		t.Fatalf("ListInstances page 1: %v", err)
	}
	if len(page1.Instances) != 2 {
		t.Fatalf("expected page 1 to have 2 instances, got %d", len(page1.Instances))
	}
	if page1.Total != 3 {
		t.Errorf("expected Total to be 3 regardless of Limit, got %d", page1.Total)
	}
	if page1.Instances[0].InstanceID != ids[0] || page1.Instances[1].InstanceID != ids[1] {
		t.Errorf("expected page 1 to be the first 2 instances in creation order, got %+v", page1.Instances)
	}

	page2, err := workflow.ListInstances(ctx, appPool, firmID, userID, definitionID, workflow.ListInstancesOptions{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("ListInstances page 2: %v", err)
	}
	if len(page2.Instances) != 1 || page2.Instances[0].InstanceID != ids[2] {
		t.Errorf("expected page 2 to be the remaining 1 instance, got %+v", page2.Instances)
	}
	if page2.Total != 3 {
		t.Errorf("expected Total to be 3 on page 2 too, got %d", page2.Total)
	}

	// Search is now full-text (migrations/0024's search_tsv,
	// plainto_tsquery('simple', ...) - see this batch's own scope
	// boundary on why not to_tsquery/websearch_to_tsquery), which matches
	// whole words/lexemes, not arbitrary substrings - "giz" alone would
	// no longer match "gizmo" the way the old ILIKE '%giz%' scan did.
	searched, err := workflow.ListInstances(ctx, appPool, firmID, userID, definitionID, workflow.ListInstancesOptions{Search: "gizmo"})
	if err != nil {
		t.Fatalf("ListInstances search: %v", err)
	}
	if len(searched.Instances) != 1 || searched.Instances[0].InstanceID != ids[2] {
		t.Errorf("expected search 'gizmo' to match only the gizmo instance, got %+v", searched.Instances)
	}
	if searched.Total != 1 {
		t.Errorf("expected search Total to be 1, got %d", searched.Total)
	}

	noMatch, err := workflow.ListInstances(ctx, appPool, firmID, userID, definitionID, workflow.ListInstancesOptions{Search: "no-such-item"})
	if err != nil {
		t.Fatalf("ListInstances search with no match: %v", err)
	}
	if len(noMatch.Instances) != 0 || noMatch.Total != 0 {
		t.Errorf("expected no matches for an unmatched search, got %+v (total %d)", noMatch.Instances, noMatch.Total)
	}
}

// --- InstanceCountsByDefinition (dashboard overview, item 2) ----------

// TestInstanceCountsByDefinition_CountsAcrossMultipleDefinitions is this
// batch's core proof for the dashboard's overview data source: two
// structurally different definitions in the same firm (Stock In -> Sale
// and Customer Pipeline), each with instances moved into a mix of
// states, and the aggregate query should report accurate per-state
// counts for both - including a zero count for a state nothing has
// reached yet (Customer Pipeline's "lost"), not an omitted row.
func TestInstanceCountsByDefinition_CountsAcrossMultipleDefinitions(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	stockDefID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed stock_to_sale workflow: %v", err)
	}
	crmDefID, err := workflow.SeedCustomerPipelineWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed customer_pipeline workflow: %v", err)
	}

	userID, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmID, "sub-counts",
		workflow.AddStockPermission, workflow.RecordSalePermission,
		workflow.CreateLeadPermission, workflow.QualifyLeadPermission, workflow.ConvertCustomerPermission, workflow.MarkLeadLostPermission,
	)

	// Stock In -> Sale: 2 in_stock, 1 sold.
	if _, err := workflow.CreateInstance(ctx, appPool, firmID, userID, stockDefID, map[string]any{"item": "widget"}); err != nil {
		t.Fatalf("CreateInstance (stock 1): %v", err)
	}
	if _, err := workflow.CreateInstance(ctx, appPool, firmID, userID, stockDefID, map[string]any{"item": "gadget"}); err != nil {
		t.Fatalf("CreateInstance (stock 2): %v", err)
	}
	soldID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, stockDefID, map[string]any{"item": "widget"})
	if err != nil {
		t.Fatalf("CreateInstance (stock 3, to sell): %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, soldID, "record_sale", nil); err != nil {
		t.Fatalf("ExecuteTransition record_sale: %v", err)
	}

	// Customer Pipeline: 1 lead, 1 customer, 0 lost, 0 qualified.
	if _, err := workflow.CreateInstance(ctx, appPool, firmID, userID, crmDefID, map[string]any{"name": "Acme Inc"}); err != nil {
		t.Fatalf("CreateInstance (lead): %v", err)
	}
	convertedID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, crmDefID, map[string]any{"name": "Beta LLC"})
	if err != nil {
		t.Fatalf("CreateInstance (to convert): %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, convertedID, "qualify", nil); err != nil {
		t.Fatalf("ExecuteTransition qualify: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, convertedID, "convert", nil); err != nil {
		t.Fatalf("ExecuteTransition convert: %v", err)
	}

	results, err := workflow.InstanceCountsByDefinition(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("InstanceCountsByDefinition: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected counts for 2 definitions, got %d: %+v", len(results), results)
	}

	byKey := make(map[string]workflow.DefinitionInstanceCounts, len(results))
	for _, r := range results {
		byKey[r.Key] = r
	}

	stock, ok := byKey[workflow.StockToSaleKey]
	if !ok {
		t.Fatalf("expected stock_to_sale counts in the result: %+v", results)
	}
	stockCounts := stateCountMap(stock.Counts)
	if stockCounts["in_stock"] != 2 {
		t.Errorf("expected 2 in_stock, got %d (%+v)", stockCounts["in_stock"], stock.Counts)
	}
	if stockCounts["sold"] != 1 {
		t.Errorf("expected 1 sold, got %d (%+v)", stockCounts["sold"], stock.Counts)
	}

	crm, ok := byKey[workflow.CustomerPipelineKey]
	if !ok {
		t.Fatalf("expected customer_pipeline counts in the result: %+v", results)
	}
	crmCounts := stateCountMap(crm.Counts)
	if crmCounts["lead"] != 1 {
		t.Errorf("expected 1 lead, got %d (%+v)", crmCounts["lead"], crm.Counts)
	}
	if crmCounts["customer"] != 1 {
		t.Errorf("expected 1 customer, got %d (%+v)", crmCounts["customer"], crm.Counts)
	}
	// "lost" and "qualified" have zero instances, but should still be
	// reported (the LEFT JOIN's whole point) rather than omitted.
	if count, ok := crmCounts["lost"]; !ok || count != 0 {
		t.Errorf("expected an explicit 0 for 'lost', got %d (present: %v, %+v)", count, ok, crm.Counts)
	}
	if count, ok := crmCounts["qualified"]; !ok || count != 0 {
		t.Errorf("expected an explicit 0 for 'qualified', got %d (present: %v, %+v)", count, ok, crm.Counts)
	}
}

func stateCountMap(counts []workflow.StateCount) map[string]int {
	m := make(map[string]int, len(counts))
	for _, c := range counts {
		m[c.StateKey] = c.Count
	}
	return m
}

// TestInstanceCountsByDefinition_RLS mirrors ListDefinitions_RLS: a
// second firm's counts must never leak into the first firm's result, and
// a non-member can't read them just by supplying a real firmID.
func TestInstanceCountsByDefinition_RLS(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmA, firmB uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA); err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB); err != nil {
		t.Fatalf("seed firm B: %v", err)
	}
	if _, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmA); err != nil {
		t.Fatalf("seed workflow A: %v", err)
	}
	crmDefIDB, err := workflow.SeedCustomerPipelineWorkflow(ctx, appPool, firmB)
	if err != nil {
		t.Fatalf("seed workflow B: %v", err)
	}
	userB, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmB, "sub-counts-b")

	resultsB, err := workflow.InstanceCountsByDefinition(ctx, appPool, firmB, userB)
	if err != nil {
		t.Fatalf("InstanceCountsByDefinition under firm B: %v", err)
	}
	if len(resultsB) != 1 || resultsB[0].DefinitionID != crmDefIDB {
		t.Fatalf("expected firm B's counts to contain only its own definition %v, got: %+v", crmDefIDB, resultsB)
	}

	if _, err := workflow.InstanceCountsByDefinition(ctx, appPool, firmA, userB); !errors.Is(err, workflow.ErrDefinitionNotFound) {
		t.Fatalf("expected ErrDefinitionNotFound when a non-member of firm A reads its counts by firm A's real ID, got: %v", err)
	}
}
