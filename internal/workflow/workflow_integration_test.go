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

	state, err := workflow.CurrentState(ctx, appPool, firmID, instanceID)
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
	state, err := workflow.CurrentState(ctx, appPool, firmID, instanceID)
	if err != nil {
		t.Fatalf("CurrentState: %v", err)
	}
	if state.State.Key != "in_stock" {
		t.Errorf("expected instance to remain in 'in_stock' after a denied transition, got %q", state.State.Key)
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

	state, err := workflow.CurrentState(ctx, appPool, firmID, instanceID)
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

	if _, err := workflow.CurrentState(ctx, appPool, firmB, instanceID); !errors.Is(err, workflow.ErrInstanceNotFound) {
		t.Fatalf("expected ErrInstanceNotFound reading Firm A's instance under Firm B's context, got: %v", err)
	}

	userB, _ := seedUserInFirm(ctx, t, adminPool, appPool, firmB, "sub-firm-b", workflow.AddStockPermission, workflow.RecordSalePermission)
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
