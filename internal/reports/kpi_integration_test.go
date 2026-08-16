// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package reports_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/reports"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run reports integration tests")
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
			workflow_definitions, workflow_states, workflow_transitions, workflow_instances,
			people, contracts, products, stock_levels, stock_movements, suppliers,
			deliveries, customers, invoices, invoice_lines, payments, invoice_sequences, audit_log, permissions,
			report_definitions, saved_report_runs CASCADE
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

func seedOwner(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmName, keycloakSubject string) (firmID, userID uuid.UUID, roleID uuid.UUID) {
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

	// Default warehouse location - mirrors migrations/0028_warehouse_management.up.sql's
	// own backfill/internal/wizard.CreateDefaultFirm's unconditional seed;
	// stock_levels.location_id (NOT NULL) needs a real row to reference.
	var warehouseID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO warehouses (firm_id, name) VALUES ($1, 'Default Warehouse') RETURNING id`, firmID).Scan(&warehouseID); err != nil {
		t.Fatalf("seed default warehouse: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO warehouse_locations (firm_id, warehouse_id, code, name, is_default) VALUES ($1, $2, 'default', 'Main', true)
	`, firmID, warehouseID); err != nil {
		t.Fatalf("seed default location: %v", err)
	}
	return firmID, userID, roleID
}

// seedLocation inserts a second, non-default warehouse_locations row
// under firmID's default warehouse - for KPI tests that need stock split
// across more than one location.
func seedLocation(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID, code string) uuid.UUID {
	t.Helper()
	var warehouseID uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT warehouse_id FROM warehouse_locations WHERE firm_id = $1 AND is_default`, firmID).Scan(&warehouseID); err != nil {
		t.Fatalf("look up default warehouse: %v", err)
	}
	var locationID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO warehouse_locations (firm_id, warehouse_id, code, name) VALUES ($1, $2, $3, $3) RETURNING id
	`, firmID, warehouseID, code).Scan(&locationID); err != nil {
		t.Fatalf("seed location: %v", err)
	}
	return locationID
}

func kpiValue(t *testing.T, results []reports.KPIResult, key string) string {
	t.Helper()
	for _, r := range results {
		if r.Key == key {
			return r.Value
		}
	}
	t.Fatalf("no KPI result for key %q, got %+v", key, results)
	return ""
}

// TestGetDashboardKPIs_AggregatesAcrossModules is the design brief's
// concrete correctness proof: seeds real accounting (a sale), a real
// task_approval instance, and real people, then confirms every KPI
// reflects that real data - not a mock, not a hardcoded stub.
func TestGetDashboardKPIs_AggregatesAcrossModules(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID, roleID := seedOwner(ctx, t, adminPool, appPool, "Firm KPI", "kpi-owner")

	// Seed the accounts a real firm's wizard would seed.
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return accounting.SeedDefaultChartOfAccountsTx(ctx, tx, firmID, accounting.SeedChartOptions{Sells: true, PurchasesFromSuppliers: true})
	})
	if err != nil {
		t.Fatalf("seed chart of accounts: %v", err)
	}

	// A real sale this month: DR Trade Receivables / CR Sales Revenue, 250.00.
	if _, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "Sale", []accounting.LineInput{
		{AccountCode: accounting.TradeReceivablesAccountCode, Side: accounting.SideDebit, Amount: "250.00"},
		{AccountCode: accounting.SalesRevenueAccountCode, Side: accounting.SideCredit, Amount: "250.00"},
	}); err != nil {
		t.Fatalf("post sale: %v", err)
	}
	// Inventory received: DR Inventory / CR Trade Payables, 100.00.
	if _, err := accounting.PostJournalEntry(ctx, appPool, firmID, userID, "Purchase received", []accounting.LineInput{
		{AccountCode: accounting.InventoryAccountCode, Side: accounting.SideDebit, Amount: "100.00"},
		{AccountCode: accounting.TradePayablesAccountCode, Side: accounting.SideCredit, Amount: "100.00"},
	}); err != nil {
		t.Fatalf("post purchase: %v", err)
	}

	// Two real task_approval instances: one open, one done (must not count).
	// SeedTaskApprovalWorkflowTx (not the pool-based SeedTaskApprovalWorkflow)
	// so the owner role actually gets task_approval's permissions granted
	// (the self-action auto-grant) - CreateInstance/ExecuteTransition
	// below need that grant to succeed as this test's owner user.
	var taskDefID uuid.UUID
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		id, err := workflow.SeedTaskApprovalWorkflowTx(ctx, tx, firmID, roleID)
		taskDefID = id
		return err
	})
	if err != nil {
		t.Fatalf("seed task_approval: %v", err)
	}
	openTaskID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, taskDefID, map[string]any{})
	if err != nil {
		t.Fatalf("create open task: %v", err)
	}
	_ = openTaskID
	doneTaskID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, taskDefID, map[string]any{})
	if err != nil {
		t.Fatalf("create task to complete: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, doneTaskID, "start", nil); err != nil {
		t.Fatalf("start task: %v", err)
	}
	if err := workflow.ExecuteTransition(ctx, appPool, firmID, userID, doneTaskID, "complete", nil); err != nil {
		t.Fatalf("complete task: %v", err)
	}

	// Two real people: one active, one inactive (must not count).
	if _, err := adminPool.Exec(ctx, `INSERT INTO people (firm_id, full_name, type, status) VALUES ($1, 'Active Person', 'employee', 'active')`, firmID); err != nil {
		t.Fatalf("seed active person: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `INSERT INTO people (firm_id, full_name, type, status) VALUES ($1, 'Inactive Person', 'employee', 'inactive')`, firmID); err != nil {
		t.Fatalf("seed inactive person: %v", err)
	}

	results, err := reports.GetDashboardKPIs(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("GetDashboardKPIs: %v", err)
	}

	if v := kpiValue(t, results, "revenueThisMonth"); v != "250.0000" {
		t.Errorf("expected revenueThisMonth 250.0000, got %q", v)
	}
	if v := kpiValue(t, results, "revenueLastMonth"); v != "0" {
		t.Errorf("expected revenueLastMonth 0, got %q", v)
	}
	if v := kpiValue(t, results, "outstandingReceivables"); v != "250.0000" {
		t.Errorf("expected outstandingReceivables 250.0000, got %q", v)
	}
	if v := kpiValue(t, results, "inventoryValue"); v != "100.0000" {
		t.Errorf("expected inventoryValue 100.0000, got %q", v)
	}
	if v := kpiValue(t, results, "openTasks"); v != "1" {
		t.Errorf("expected openTasks 1 (the completed one must not count), got %q", v)
	}
	if v := kpiValue(t, results, "activePeople"); v != "1" {
		t.Errorf("expected activePeople 1 (the inactive one must not count), got %q", v)
	}
}

// TestGetDashboardKPIs_InventoryMetrics confirms the Inventory management
// batch's two new inventory_kpi descriptors (lowStockProducts/
// totalInventoryValue) compute correctly: a product below its own
// min_quantity threshold (summed across every location, including one
// with no stock_levels row at all) counts toward lowStockProducts, a
// product at/above its threshold or with no threshold set does not, and
// totalInventoryValue sums quantity * cost_price across every
// stock_levels row regardless of any product's own low-stock status.
func TestGetDashboardKPIs_InventoryMetrics(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm Inventory KPI", "kpi-inventory-owner")

	var lowStockNoStockID, lowStockWithStockID, healthyStockID, noThresholdID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO products (firm_id, sku, name, cost_price, min_quantity) VALUES ($1, 'LOW-NO-STOCK', 'Low, no stock rows', '10.00', 5) RETURNING id
	`, firmID).Scan(&lowStockNoStockID); err != nil {
		t.Fatalf("seed product (low, no stock): %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO products (firm_id, sku, name, cost_price, min_quantity) VALUES ($1, 'LOW-WITH-STOCK', 'Low, below threshold', '4.00', 10) RETURNING id
	`, firmID).Scan(&lowStockWithStockID); err != nil {
		t.Fatalf("seed product (low, with stock): %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO products (firm_id, sku, name, cost_price, min_quantity) VALUES ($1, 'HEALTHY', 'At or above threshold', '2.00', 5) RETURNING id
	`, firmID).Scan(&healthyStockID); err != nil {
		t.Fatalf("seed product (healthy): %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO products (firm_id, sku, name, cost_price, min_quantity) VALUES ($1, 'NO-THRESHOLD', 'No threshold set', '3.00', 0) RETURNING id
	`, firmID).Scan(&noThresholdID); err != nil {
		t.Fatalf("seed product (no threshold): %v", err)
	}

	warehouse2ID := seedLocation(ctx, t, adminPool, firmID, "warehouse-2")

	// lowStockWithStockID: 3 below its threshold of 10.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO stock_levels (firm_id, product_id, location_id, quantity)
		SELECT $1, $2, id, 3 FROM warehouse_locations WHERE firm_id = $1 AND is_default
	`, firmID, lowStockWithStockID); err != nil {
		t.Fatalf("seed stock (low, with stock): %v", err)
	}
	// healthyStockID: 5 total across two locations, exactly at its
	// threshold of 5 - "at" the threshold is NOT low (strict <), so this
	// must not count toward lowStockProducts.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO stock_levels (firm_id, product_id, location_id, quantity)
		SELECT $1, $2, id, 3 FROM warehouse_locations WHERE firm_id = $1 AND is_default
	`, firmID, healthyStockID); err != nil {
		t.Fatalf("seed stock (healthy, default location): %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO stock_levels (firm_id, product_id, location_id, quantity) VALUES ($1, $2, $3, 2)
	`, firmID, healthyStockID, warehouse2ID); err != nil {
		t.Fatalf("seed stock (healthy, warehouse-2): %v", err)
	}
	// noThresholdID: has some stock, but min_quantity is 0 - never flagged
	// low regardless of on-hand quantity (products.min_quantity's own
	// documented default, migrations/0011_inventory_core.up.sql).
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO stock_levels (firm_id, product_id, location_id, quantity)
		SELECT $1, $2, id, 0 FROM warehouse_locations WHERE firm_id = $1 AND is_default
	`, firmID, noThresholdID); err != nil {
		t.Fatalf("seed stock (no threshold): %v", err)
	}
	// lowStockNoStockID gets NO stock_levels row at all - on-hand
	// quantity is implicitly 0, still below its threshold of 5.

	results, err := reports.GetDashboardKPIs(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("GetDashboardKPIs: %v", err)
	}

	if v := kpiValue(t, results, "lowStockProducts"); v != "2" {
		t.Errorf("expected lowStockProducts 2 (LOW-NO-STOCK and LOW-WITH-STOCK), got %q", v)
	}
	// 3*4.00 (LOW-WITH-STOCK) + (3*2.00 + 2*2.00) (HEALTHY) + 0*3.00 (NO-THRESHOLD) = 12.00 + 10.00 + 0 = 22.00.
	if v := kpiValue(t, results, "totalInventoryValue"); v != "22.0000" {
		t.Errorf("expected totalInventoryValue 22.0000, got %q", v)
	}
}

// TestGetDashboardKPIs_LogisticsCRMMetrics confirms the Logistics/CRM
// batch's two new descriptors: pendingDeliveries (KPIKindInventoryKPI,
// this batch's own operational-metric classification - see
// InventoryMetricPendingDeliveries' own doc comment, kpi.go) counts only
// 'pending'/'in_transit' deliveries, not 'delivered'/'returned'/'cancelled'
// ones; activeCustomers (the new KPIKindCRM) counts only customers with a
// non-null source_workflow_instance, not manually-created ones.
func TestGetDashboardKPIs_LogisticsCRMMetrics(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID, roleID := seedOwner(ctx, t, adminPool, appPool, "Firm Logistics CRM KPI", "kpi-logistics-crm-owner")

	if _, err := adminPool.Exec(ctx, `
		INSERT INTO deliveries (firm_id, destination_address, status) VALUES
			($1, 'Addr 1', 'pending'),
			($1, 'Addr 2', 'in_transit'),
			($1, 'Addr 3', 'delivered'),
			($1, 'Addr 4', 'cancelled')
	`, firmID); err != nil {
		t.Fatalf("seed deliveries: %v", err)
	}

	// A manually-created customer (source_workflow_instance NULL) must
	// NOT count toward activeCustomers.
	if _, err := adminPool.Exec(ctx, `INSERT INTO customers (firm_id, name) VALUES ($1, 'Manual Customer')`, firmID); err != nil {
		t.Fatalf("seed manual customer: %v", err)
	}

	// A pipeline-converted customer needs a real workflow_instances row to
	// reference (source_workflow_instance is a real FK). Seeded via
	// SeedCustomerPipelineWorkflowTx (not the pool-based
	// SeedCustomerPipelineWorkflow) so the owner role actually gets
	// customer_pipeline's permissions granted (the self-action
	// auto-grant) - CreateInstance below needs that grant to succeed as
	// this test's owner user, the same reasoning
	// TestGetDashboardKPIs_AggregatesAcrossModules' own task_approval
	// seeding gives.
	var leadDefinitionID uuid.UUID
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		id, err := workflow.SeedCustomerPipelineWorkflowTx(ctx, tx, firmID, roleID)
		leadDefinitionID = id
		return err
	})
	if err != nil {
		t.Fatalf("seed customer_pipeline: %v", err)
	}
	instanceID, err := workflow.CreateInstance(ctx, appPool, firmID, userID, leadDefinitionID, map[string]any{"name": "Pipeline Customer"})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO customers (firm_id, name, source_workflow_instance) VALUES ($1, 'Pipeline Customer', $2)
	`, firmID, instanceID); err != nil {
		t.Fatalf("seed pipeline customer: %v", err)
	}

	results, err := reports.GetDashboardKPIs(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("GetDashboardKPIs: %v", err)
	}

	if v := kpiValue(t, results, "pendingDeliveries"); v != "2" {
		t.Errorf("expected pendingDeliveries 2 (pending + in_transit only), got %q", v)
	}
	if v := kpiValue(t, results, "activeCustomers"); v != "1" {
		t.Errorf("expected activeCustomers 1 (the pipeline-converted one only), got %q", v)
	}
}

// TestGetDashboardKPIs_ReceivablesMetrics seeds a small mix of invoices
// (draft, sent-not-yet-due, sent-and-overdue, explicitly overdue, paid,
// cancelled - plus one sent invoice with a partial payment) and confirms
// overdueInvoices/totalOutstanding both reflect exactly the invoices that
// should count.
func TestGetDashboardKPIs_ReceivablesMetrics(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm Receivables KPI", "kpi-receivables-owner")

	// draft: not a real receivable yet - excluded from both metrics.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, issued_date, due_date, status, subtotal, tax_amount, total)
		VALUES ($1, 'INV-D1', CURRENT_DATE, CURRENT_DATE + 30, 'draft', 100, 0, 100)
	`, firmID); err != nil {
		t.Fatalf("seed draft invoice: %v", err)
	}

	// sent, due_date in the future: outstanding, but NOT overdue.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, issued_date, due_date, status, subtotal, tax_amount, total)
		VALUES ($1, 'INV-S1', CURRENT_DATE, CURRENT_DATE + 30, 'sent', 100, 0, 100)
	`, firmID); err != nil {
		t.Fatalf("seed not-yet-due invoice: %v", err)
	}

	// sent, due_date in the past: overdue IN SUBSTANCE even though status
	// is still 'sent' - must count toward overdueInvoices.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, issued_date, due_date, status, subtotal, tax_amount, total)
		VALUES ($1, 'INV-S2', CURRENT_DATE - 60, CURRENT_DATE - 30, 'sent', 200, 0, 200)
	`, firmID); err != nil {
		t.Fatalf("seed substantively-overdue invoice: %v", err)
	}

	// explicitly 'overdue' status.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, issued_date, due_date, status, subtotal, tax_amount, total)
		VALUES ($1, 'INV-O1', CURRENT_DATE - 90, CURRENT_DATE - 60, 'overdue', 300, 0, 300)
	`, firmID); err != nil {
		t.Fatalf("seed overdue invoice: %v", err)
	}

	// sent, with a partial payment: outstanding = total - paid, not just total.
	var partiallyPaidID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, issued_date, due_date, status, subtotal, tax_amount, total)
		VALUES ($1, 'INV-S3', CURRENT_DATE, CURRENT_DATE + 30, 'sent', 500, 0, 500)
		RETURNING id
	`, firmID).Scan(&partiallyPaidID); err != nil {
		t.Fatalf("seed partially-paid invoice: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO payments (firm_id, invoice_id, amount, paid_at) VALUES ($1, $2, 150, now())
	`, firmID, partiallyPaidID); err != nil {
		t.Fatalf("seed partial payment: %v", err)
	}

	// paid and cancelled: no longer outstanding - excluded from both metrics.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, issued_date, due_date, status, subtotal, tax_amount, total)
		VALUES ($1, 'INV-P1', CURRENT_DATE - 10, CURRENT_DATE - 5, 'paid', 400, 0, 400)
	`, firmID); err != nil {
		t.Fatalf("seed paid invoice: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO invoices (firm_id, invoice_number, issued_date, due_date, status, subtotal, tax_amount, total)
		VALUES ($1, 'INV-C1', CURRENT_DATE - 10, CURRENT_DATE - 5, 'cancelled', 999, 0, 999)
	`, firmID); err != nil {
		t.Fatalf("seed cancelled invoice: %v", err)
	}

	results, err := reports.GetDashboardKPIs(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("GetDashboardKPIs: %v", err)
	}

	// Overdue: INV-S2 (sent, past due) + INV-O1 (explicitly overdue) = 2.
	if v := kpiValue(t, results, "overdueInvoices"); v != "2" {
		t.Errorf("expected overdueInvoices 2, got %q", v)
	}
	// Outstanding: 100 (INV-S1) + 200 (INV-S2) + 300 (INV-O1) + 350 (INV-S3
	// net of its 150 payment) = 950.0000. Draft/paid/cancelled excluded.
	if v := kpiValue(t, results, "totalOutstanding"); v != "950.0000" {
		t.Errorf("expected totalOutstanding 950.0000, got %q", v)
	}
}

// TestGetDashboardKPIs_EmptyFirmReadsZeros confirms a brand-new firm with
// no accounts/workflows/people yet reads back real zeros, not errors -
// see GetDashboardKPIs' own doc comment for why that's the correct
// empty-state answer.
func TestGetDashboardKPIs_EmptyFirmReadsZeros(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID, _ := seedOwner(ctx, t, adminPool, appPool, "Firm Empty", "kpi-empty-owner")

	results, err := reports.GetDashboardKPIs(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("GetDashboardKPIs: %v", err)
	}
	if len(results) != 16 {
		t.Fatalf("expected 16 KPI results, got %d: %+v", len(results), results)
	}
	for _, r := range results {
		// A plain "0" or a zero-valued decimal string ("0.0000") both mean
		// "zero" here - which one a given Kind's query produces depends on
		// whether it casts its SUM to a fixed scale (e.g.
		// totalInventoryValue's own numeric(19,4) cast, kpi.go) or leaves
		// Postgres's default numeric formatting alone (every other
		// currency KPI here) - both are the same "real zero, not an
		// error" empty-state answer this test exists to confirm, so this
		// check is float-value-based, not a literal string match.
		value, err := strconv.ParseFloat(r.Value, 64)
		if err != nil || value != 0 {
			t.Errorf("expected KPI %q to be 0 for an empty firm, got %q", r.Key, r.Value)
		}
	}
}
