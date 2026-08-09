// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package portability_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/portability"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// These tests exercise export/import against a real Postgres instance -
// RLS isolation and permission enforcement can't be meaningfully faked,
// same convention as every other package's integration tests in this
// codebase. Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL and
// ZONARYOS_TEST_APP_DATABASE_URL are set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run portability integration tests")
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
			accounts, journal_entries, journal_lines,
			workflow_definitions, workflow_states, workflow_transitions, workflow_instances, workflow_rules,
			people, contracts, products, stock_levels, stock_movements, suppliers,
			deliveries, customers, invoices, invoice_lines, payments, invoice_sequences,
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

func seedMember(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmID uuid.UUID, keycloakSubject string) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test Member").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO roles (firm_id, key, name) VALUES ($1, 'member', 'Member') RETURNING id`, firmID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed member role/membership: %v", err)
	}
	return userID
}

// minimalRule is a structurally valid Rule (ValidateExpressionTree/
// ValidateActions both accept it) for definitionKey - mirrors
// internal/workflow's own rules_integration_test.go fixtures.
func minimalRule(firmID uuid.UUID, definitionKey, name string) workflow.Rule {
	return workflow.Rule{
		FirmID: firmID, DefinitionKey: definitionKey, Name: name, Trigger: workflow.TriggerOnCreate,
		ConditionTree: workflow.ExpressionNode{Type: workflow.ConditionField, Field: "quantity", Op: string(workflow.OpGt), Value: float64(0)},
		Actions:       []workflow.Action{{Type: workflow.ActionNotify, Channel: workflow.NotifyChannelAuditLog, MessageTemplate: "x"}},
		Autonomous:    true, Enabled: true,
	}
}

func TestExportFirm_StructureAndAuditLog(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, roleID := seedOwner(ctx, t, adminPool, appPool, "Firm Export", "export-owner")
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "export-member")

	if _, err := accounting.CreateAccount(ctx, appPool, firmID, ownerID, "1000", "Cash", accounting.AccountTypeAsset, nil, "TRY"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := inventory.CreateProduct(ctx, appPool, firmID, ownerID, inventory.CreateProductInput{SKU: "SKU-1", Name: "Widget"}); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := workflow.SeedStockToSaleWorkflowTx(ctx, tx, firmID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed stock_to_sale: %v", err)
	}
	if _, err := workflow.CreateRuleForFirm(ctx, appPool, firmID, ownerID, workflow.StockToSaleKey, minimalRule(firmID, workflow.StockToSaleKey, "Export Rule")); err != nil {
		t.Fatalf("seed rule: %v", err)
	}

	// A non-owner must be rejected.
	if _, err := portability.ExportFirm(ctx, appPool, firmID, memberID); !errors.Is(err, portability.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a non-owner export, got %v", err)
	}

	doc, err := portability.ExportFirm(ctx, appPool, firmID, ownerID)
	if err != nil {
		t.Fatalf("ExportFirm: %v", err)
	}
	if doc.Version != portability.ExportVersion {
		t.Errorf("expected version %q, got %q", portability.ExportVersion, doc.Version)
	}
	if doc.Firm.Name != "Firm Export" {
		t.Errorf("expected firm name %q, got %q", "Firm Export", doc.Firm.Name)
	}
	if len(doc.Accounts) != 1 || doc.Accounts[0].Code != "1000" {
		t.Fatalf("expected exactly the one seeded account (1000), got %+v", doc.Accounts)
	}
	if len(doc.Products) != 1 || doc.Products[0].SKU != "SKU-1" {
		t.Fatalf("expected exactly the one seeded product (SKU-1), got %+v", doc.Products)
	}
	var stockDef *workflow.DefinitionSpec
	for i := range doc.WorkflowDefinitions {
		if doc.WorkflowDefinitions[i].Key == workflow.StockToSaleKey {
			stockDef = &doc.WorkflowDefinitions[i]
		}
	}
	if stockDef == nil {
		t.Fatalf("expected stock_to_sale among exported workflow definitions, got %+v", doc.WorkflowDefinitions)
	}
	if len(stockDef.States) != 2 || len(stockDef.Transitions) != 1 {
		t.Fatalf("expected stock_to_sale's 2 states / 1 transition to round-trip, got %+v", stockDef)
	}
	if len(stockDef.Transitions[0].Effects) == 0 {
		t.Errorf("expected record_sale's Effects to be non-empty (Journal/StockAdjustment/Delivery/Invoice), got none")
	}
	if len(doc.Rules) != 1 || doc.Rules[0].Name != "Export Rule" {
		t.Fatalf("expected exactly the one seeded rule, got %+v", doc.Rules)
	}

	// The audit trail recorded this export.
	var count int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log WHERE firm_id = $1 AND entity_type = 'firm_export' AND action = 'export'
	`, firmID).Scan(&count); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 export audit_log entry, got %d", count)
	}
}

func TestImportConfiguration_Idempotent(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	sourceFirmID, sourceOwnerID, sourceRoleID := seedOwner(ctx, t, adminPool, appPool, "Firm Source", "import-source-owner")
	if _, err := accounting.CreateAccount(ctx, appPool, sourceFirmID, sourceOwnerID, "1000", "Cash", accounting.AccountTypeAsset, nil, "TRY"); err != nil {
		t.Fatalf("seed source account: %v", err)
	}
	if _, err := inventory.CreateProduct(ctx, appPool, sourceFirmID, sourceOwnerID, inventory.CreateProductInput{SKU: "SKU-1", Name: "Widget"}); err != nil {
		t.Fatalf("seed source product: %v", err)
	}
	err := zdb.WithFirmContext(ctx, appPool, sourceFirmID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := workflow.SeedStockToSaleWorkflowTx(ctx, tx, sourceFirmID, sourceRoleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed source stock_to_sale: %v", err)
	}
	if _, err := workflow.CreateRuleForFirm(ctx, appPool, sourceFirmID, sourceOwnerID, workflow.StockToSaleKey, minimalRule(sourceFirmID, workflow.StockToSaleKey, "Imported Rule")); err != nil {
		t.Fatalf("seed source rule: %v", err)
	}

	doc, err := portability.ExportFirm(ctx, appPool, sourceFirmID, sourceOwnerID)
	if err != nil {
		t.Fatalf("ExportFirm (source): %v", err)
	}

	targetFirmID, targetOwnerID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm Target", "import-target-owner")
	targetMemberID := seedMember(ctx, t, adminPool, appPool, targetFirmID, "import-target-member")

	if _, err := portability.ImportConfiguration(ctx, appPool, targetFirmID, targetMemberID, doc); !errors.Is(err, portability.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a non-owner import, got %v", err)
	}

	first, err := portability.ImportConfiguration(ctx, appPool, targetFirmID, targetOwnerID, doc)
	if err != nil {
		t.Fatalf("ImportConfiguration (first): %v", err)
	}
	if first.WorkflowDefinitions.Imported != 1 || first.WorkflowDefinitions.Skipped != 0 {
		t.Errorf("expected 1 workflow definition imported on the first pass, got %+v", first.WorkflowDefinitions)
	}
	if first.Rules.Imported != 1 || first.Accounts.Imported != 1 || first.Products.Imported != 1 {
		t.Fatalf("expected 1 rule/account/product imported on the first pass, got %+v", first)
	}

	second, err := portability.ImportConfiguration(ctx, appPool, targetFirmID, targetOwnerID, doc)
	if err != nil {
		t.Fatalf("ImportConfiguration (second): %v", err)
	}
	if second.WorkflowDefinitions.Imported != 0 || second.WorkflowDefinitions.Skipped != 1 {
		t.Errorf("expected the second import to skip the already-imported workflow definition, got %+v", second.WorkflowDefinitions)
	}
	if second.Rules.Imported != 0 || second.Rules.Skipped != 1 {
		t.Errorf("expected the second import to skip the already-imported rule, got %+v", second.Rules)
	}
	if second.Accounts.Imported != 0 || second.Accounts.Skipped != 1 {
		t.Errorf("expected the second import to skip the already-imported account, got %+v", second.Accounts)
	}
	if second.Products.Imported != 0 || second.Products.Skipped != 1 {
		t.Errorf("expected the second import to skip the already-imported product, got %+v", second.Products)
	}

	// Actual row counts in the target firm must match a single import,
	// not two.
	var accountCount, productCount, ruleCount, defCount int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE firm_id = $1`, targetFirmID).Scan(&accountCount); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM products WHERE firm_id = $1`, targetFirmID).Scan(&productCount); err != nil {
		t.Fatalf("count products: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM workflow_rules WHERE firm_id = $1`, targetFirmID).Scan(&ruleCount); err != nil {
		t.Fatalf("count rules: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM workflow_definitions WHERE firm_id = $1`, targetFirmID).Scan(&defCount); err != nil {
		t.Fatalf("count definitions: %v", err)
	}
	if accountCount != 1 || productCount != 1 || ruleCount != 1 || defCount != 1 {
		t.Fatalf("expected exactly 1 row each after importing twice, got accounts=%d products=%d rules=%d definitions=%d",
			accountCount, productCount, ruleCount, defCount)
	}
}

func TestImportConfiguration_RollsBackOnPartialFailure(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm Rollback", "import-rollback-owner")

	doc := portability.ExportDocument{
		Version: portability.ExportVersion,
		Accounts: []accounting.Account{
			{Code: "1000", Name: "Cash", Type: accounting.AccountTypeAsset, Currency: "TRY"},
			// A structurally invalid account (empty code) - CreateAccountTx
			// rejects this with ErrInvalidAccount, a real failure (not a
			// skip-worthy collision), which must abort the WHOLE import
			// transaction - including the perfectly valid "1000" account
			// immediately above it, inserted earlier in the same loop.
			{Code: "", Name: "Bad Account", Type: accounting.AccountTypeAsset, Currency: "TRY"},
		},
	}

	if _, err := portability.ImportConfiguration(ctx, appPool, firmID, ownerID, doc); err == nil {
		t.Fatal("expected ImportConfiguration to fail on the invalid second account")
	}

	var accountCount int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE firm_id = $1`, firmID).Scan(&accountCount); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if accountCount != 0 {
		t.Fatalf("expected the valid account inserted earlier in the same failed import to have been rolled back too, got %d accounts", accountCount)
	}

	var auditCount int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log WHERE firm_id = $1 AND entity_type = 'firm_import'
	`, firmID).Scan(&auditCount); err != nil {
		t.Fatalf("count audit_log: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("expected no import audit_log entry either, since the whole transaction rolled back, got %d", auditCount)
	}
}
