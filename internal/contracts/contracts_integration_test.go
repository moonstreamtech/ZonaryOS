// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package contracts_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/contracts"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// These tests exercise internal/contracts against a real Postgres
// instance - RLS isolation and permission enforcement can't be
// meaningfully faked, same convention every other package's integration
// tests in this codebase follow. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run contracts integration tests")
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
			contract_registry, contract_documents, notifications, document_templates,
			workflow_definitions, workflow_states, workflow_transitions, workflow_instances,
			audit_log, permissions CASCADE
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

func seedOwner(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmName, keycloakSubject string) (firmID, userID, roleID uuid.UUID) {
	t.Helper()

	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ($1) RETURNING id`, firmName).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test Owner").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (firm_id, key, name, is_owner) VALUES ($1, 'owner', 'Owner', true) RETURNING id
		`, firmID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed owner role/membership: %v", err)
	}
	return firmID, userID, roleID
}

// TestListExpiringContracts_ReturnsCorrectSubset confirms the expiry
// query returns only active, non-auto-renewing contracts whose end_date
// falls within the requested window - not draft/terminated contracts,
// not auto-renewing ones, not contracts expiring further out.
func TestListExpiringContracts_ReturnsCorrectSubset(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm A", "expiry-owner")

	soon, err := contracts.CreateContract(ctx, appPool, firmID, ownerID, contracts.CreateContractInput{
		ReferenceNumber: "C-001", Title: "Expiring Soon", Type: "supplier", CounterpartyName: "Acme Supply",
		Status: "active", EndDate: time.Now().AddDate(0, 0, 10).Format("2006-01-02"),
	})
	if err != nil {
		t.Fatalf("CreateContract (soon): %v", err)
	}

	// Auto-renewing - must NOT appear even though its end_date is close.
	if _, err := contracts.CreateContract(ctx, appPool, firmID, ownerID, contracts.CreateContractInput{
		ReferenceNumber: "C-002", Title: "Auto Renews", Type: "service", CounterpartyName: "Acme Service",
		Status: "active", EndDate: time.Now().AddDate(0, 0, 5).Format("2006-01-02"), AutoRenewal: true,
	}); err != nil {
		t.Fatalf("CreateContract (auto-renewal): %v", err)
	}

	// Draft - must NOT appear even with a near end_date.
	if _, err := contracts.CreateContract(ctx, appPool, firmID, ownerID, contracts.CreateContractInput{
		ReferenceNumber: "C-003", Title: "Still Draft", Type: "customer", CounterpartyName: "Acme Customer",
		Status: "draft", EndDate: time.Now().AddDate(0, 0, 5).Format("2006-01-02"),
	}); err != nil {
		t.Fatalf("CreateContract (draft): %v", err)
	}

	// Active but far in the future - must NOT appear within a 30-day window.
	if _, err := contracts.CreateContract(ctx, appPool, firmID, ownerID, contracts.CreateContractInput{
		ReferenceNumber: "C-004", Title: "Far Out", Type: "nda", CounterpartyName: "Acme NDA",
		Status: "active", EndDate: time.Now().AddDate(0, 6, 0).Format("2006-01-02"),
	}); err != nil {
		t.Fatalf("CreateContract (far out): %v", err)
	}

	expiring, err := contracts.ListExpiringContracts(ctx, appPool, firmID, ownerID, 30)
	if err != nil {
		t.Fatalf("ListExpiringContracts: %v", err)
	}
	if len(expiring) != 1 {
		t.Fatalf("expected exactly 1 expiring contract, got %d: %+v", len(expiring), expiring)
	}
	if expiring[0].ID != soon.ID {
		t.Errorf("expected the expiring contract to be %s, got %s", soon.ID, expiring[0].ID)
	}
}

// TestProcessExpiringContracts_NotifiesOwnersAndDedupsSameDay confirms
// the background scheduler's testable core (ProcessExpiringContracts)
// finds an active, non-auto-renewing contract within its own
// renewal_notice_days window and creates a real notification for the
// firm's owner, and that a second same-day poll does not duplicate it.
func TestProcessExpiringContracts_NotifiesOwnersAndDedupsSameDay(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm A", "scheduler-owner")

	noticeDays := 5
	c, err := contracts.CreateContract(ctx, appPool, firmID, ownerID, contracts.CreateContractInput{
		ReferenceNumber: "C-100", Title: "Cleaning Service", Type: "service", CounterpartyName: "Acme Cleaning",
		Status: "active", EndDate: time.Now().AddDate(0, 0, 3).Format("2006-01-02"), RenewalNoticeDays: &noticeDays,
	})
	if err != nil {
		t.Fatalf("CreateContract: %v", err)
	}

	contracts.ProcessExpiringContracts(ctx, appPool)

	var count int
	var payloadContractID string
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM notifications WHERE firm_id = $1 AND recipient_user_id = $2 AND type = 'contract_expiring'
		`, firmID, ownerID).Scan(&count); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT payload->>'contractId' FROM notifications WHERE firm_id = $1 AND type = 'contract_expiring' LIMIT 1
		`, firmID).Scan(&payloadContractID)
	})
	if err != nil {
		t.Fatalf("look up notification: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 contract_expiring notification for the owner, got %d", count)
	}
	if payloadContractID != c.ID.String() {
		t.Errorf("expected notification payload to reference contract %s, got %q", c.ID, payloadContractID)
	}

	// Same-day dedup: a second poll must not create a duplicate.
	contracts.ProcessExpiringContracts(ctx, appPool)
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE firm_id = $1 AND type = 'contract_expiring'`, firmID).Scan(&count)
	})
	if err != nil {
		t.Fatalf("re-count notifications: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected still exactly 1 notification after a second same-day poll (dedup), got %d", count)
	}
}

// TestProcessExpiringContracts_AutoRenewalSkipsNotification confirms an
// auto-renewing contract - even one expiring imminently - never
// generates a notification, the whole point of the auto_renewal flag.
func TestProcessExpiringContracts_AutoRenewalSkipsNotification(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm A", "auto-renewal-owner")

	if _, err := contracts.CreateContract(ctx, appPool, firmID, ownerID, contracts.CreateContractInput{
		ReferenceNumber: "C-200", Title: "Auto Renewing Lease", Type: "other", CounterpartyName: "Acme Landlord",
		Status: "active", EndDate: time.Now().AddDate(0, 0, 1).Format("2006-01-02"), AutoRenewal: true,
	}); err != nil {
		t.Fatalf("CreateContract: %v", err)
	}

	contracts.ProcessExpiringContracts(ctx, appPool)

	var count int
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE firm_id = $1 AND type = 'contract_expiring'`, firmID).Scan(&count)
	})
	if err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no notification for an auto-renewing contract, got %d", count)
	}
}

// TestFieldTypeContract_ValidatesContractReference confirms a workflow
// field of type "contract" rejects a nonexistent contract ID and accepts
// a real one - internal/workflow's own contractValidator, exercised
// end-to-end through a minimal ad hoc workflow definition, same pattern
// internal/asset's own TestFieldTypeAsset_ValidatesAssetReference uses.
func TestFieldTypeContract_ValidatesContractReference(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm A", "field-contract-owner")

	c, err := contracts.CreateContract(ctx, appPool, firmID, ownerID, contracts.CreateContractInput{
		ReferenceNumber: "C-300", Title: "Master Sales Agreement", Type: "customer", CounterpartyName: "Acme Customer",
	})
	if err != nil {
		t.Fatalf("CreateContract: %v", err)
	}

	spec := workflow.DefinitionSpec{
		Key: "sales_order_governed", Name: "Governed Sales Order",
		CreatePermission: workflow.PermissionSpec{Key: "workflow.sales_order_governed.create"},
		Fields:           []workflow.FieldSpec{{Name: "contract_id", Type: workflow.FieldTypeContract, Required: true}},
		States: []workflow.StateSpec{
			{Key: "open", Name: "Open", IsInitial: true},
			{Key: "closed", Name: "Closed", IsTerminal: true},
		},
		Transitions: []workflow.TransitionSpec{{
			ActionKey: "close", Name: "Close", FromStateKey: "open", ToStateKey: "closed",
			Permission: workflow.PermissionSpec{Key: "workflow.sales_order_governed.close"},
		}},
	}
	defID, err := workflow.DefineWorkflowForFirm(ctx, appPool, firmID, ownerID, spec)
	if err != nil {
		t.Fatalf("DefineWorkflowForFirm: %v", err)
	}

	if _, err := workflow.CreateInstance(ctx, appPool, firmID, ownerID, defID, map[string]any{"contract_id": uuid.New().String()}); err == nil {
		t.Fatal("expected CreateInstance to reject a nonexistent contract_id")
	}

	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, ownerID, defID, map[string]any{"contract_id": c.ID.String()})
	if err != nil {
		t.Fatalf("expected CreateInstance to accept a real contract_id: %v", err)
	}
	if instanceID == uuid.Nil {
		t.Fatal("expected a real instance id")
	}
}
