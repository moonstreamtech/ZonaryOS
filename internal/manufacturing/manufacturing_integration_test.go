// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package manufacturing_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/manufacturing"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise internal/manufacturing against a real Postgres
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
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run manufacturing integration tests")
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
			bom_headers, bom_lines, production_orders, production_order_material_issues, production_order_sequences,
			products, stock_levels, stock_movements, accounts, journal_entries, journal_lines, audit_log, permissions CASCADE
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

func seedOwner(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmName, keycloakSubject string) (firmID, userID uuid.UUID) {
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
		var roleID uuid.UUID
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

	// Default warehouse location - StartProductionOrder/CompleteProductionOrder
	// call AdjustStockTx with a nil locationID, which requires firmID to
	// have a warehouse_locations row flagged is_default (mirrors
	// migrations/0028_warehouse_management.up.sql's own backfill/
	// internal/wizard.CreateDefaultFirm's unconditional seed).
	var warehouseID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO warehouses (firm_id, name) VALUES ($1, 'Default Warehouse') RETURNING id`, firmID).Scan(&warehouseID); err != nil {
		t.Fatalf("seed default warehouse: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO warehouse_locations (firm_id, warehouse_id, code, name, is_default) VALUES ($1, $2, 'default', 'Main', true)
	`, firmID, warehouseID); err != nil {
		t.Fatalf("seed default location: %v", err)
	}
	return firmID, userID
}

func seedProduct(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID, ownerID uuid.UUID, sku, name, costPrice string) uuid.UUID {
	t.Helper()
	p, err := inventory.CreateProduct(ctx, appPool, firmID, ownerID, inventory.CreateProductInput{SKU: sku, Name: name, CostPrice: costPrice})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	return p.ID
}

func seedStock(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID, productID uuid.UUID, quantity string) {
	t.Helper()
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return inventory.AdjustStockTx(ctx, tx, firmID, productID, nil, quantity, "purchase", "test", nil)
	})
	if err != nil {
		t.Fatalf("seed stock: %v", err)
	}
}

func stockQuantity(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID, productID uuid.UUID) string {
	t.Helper()
	var qty string
	if err := adminPool.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity), 0)::text FROM stock_levels WHERE firm_id = $1 AND product_id = $2
	`, firmID, productID).Scan(&qty); err != nil {
		t.Fatalf("read stock quantity: %v", err)
	}
	return qty
}

func seedManufacturingAccounts(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID uuid.UUID) {
	t.Helper()
	// Mirrors internal/payroll's own seedPeopleAccounts test helper -
	// seeds just the accounts CompleteProductionOrder's journal posting
	// needs, without going through the full wizard.
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		for _, a := range []struct{ code, name, typ string }{
			{"1200", "Inventory", "asset"},
			{"1300", "Work in Progress", "asset"},
			{"1400", "Finished Goods", "asset"},
		} {
			if _, err := tx.Exec(ctx, `INSERT INTO accounts (firm_id, code, name, type) VALUES ($1, $2, $3, $4)`, firmID, a.code, a.name, a.typ); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed manufacturing accounts: %v", err)
	}
}

func createBOM(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID, ownerID, productID, componentID uuid.UUID, quantityPerUnit string, isActive *bool) manufacturing.BOMHeader {
	t.Helper()
	bom, err := manufacturing.CreateBOM(ctx, appPool, firmID, ownerID, manufacturing.CreateBOMInput{
		ProductID: productID, Name: "Widget BOM", IsActive: isActive,
		Lines: []manufacturing.BOMLineInput{{ComponentProductID: componentID, QuantityPerUnit: quantityPerUnit}},
	})
	if err != nil {
		t.Fatalf("CreateBOM: %v", err)
	}
	return bom
}

// TestCreateBOM_ActiveVersionEnforcement is this batch's own required
// test case: setting a new BOM version active deactivates every other
// version for the same product, so at most one is ever active at once.
func TestCreateBOM_ActiveVersionEnforcement(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "bom-active-owner")
	productID := seedProduct(ctx, t, appPool, firmID, ownerID, "WIDGET-1", "Widget", "")
	componentID := seedProduct(ctx, t, appPool, firmID, ownerID, "PART-1", "Part", "1.00")

	v1, err := manufacturing.CreateBOM(ctx, appPool, firmID, ownerID, manufacturing.CreateBOMInput{
		ProductID: productID, Name: "Widget BOM", Version: "1.0",
		Lines: []manufacturing.BOMLineInput{{ComponentProductID: componentID, QuantityPerUnit: "2"}},
	})
	if err != nil {
		t.Fatalf("CreateBOM v1: %v", err)
	}
	if !v1.IsActive {
		t.Fatal("expected v1 to be active by default")
	}

	v2, err := manufacturing.CreateBOM(ctx, appPool, firmID, ownerID, manufacturing.CreateBOMInput{
		ProductID: productID, Name: "Widget BOM", Version: "2.0",
		Lines: []manufacturing.BOMLineInput{{ComponentProductID: componentID, QuantityPerUnit: "3"}},
	})
	if err != nil {
		t.Fatalf("CreateBOM v2: %v", err)
	}
	if !v2.IsActive {
		t.Fatal("expected v2 to be active (the default for a newly-created BOM)")
	}

	v1Reread, err := manufacturing.GetBOM(ctx, appPool, firmID, ownerID, v1.ID)
	if err != nil {
		t.Fatalf("GetBOM v1: %v", err)
	}
	if v1Reread.IsActive {
		t.Error("expected v1 to have been deactivated when v2 was created active")
	}

	// Explicitly re-activating v1 via UpdateBOM should deactivate v2 in turn.
	isActive := true
	if _, err := manufacturing.UpdateBOM(ctx, appPool, firmID, ownerID, v1.ID, manufacturing.UpdateBOMInput{IsActive: &isActive}); err != nil {
		t.Fatalf("UpdateBOM v1 active: %v", err)
	}
	v2Reread, err := manufacturing.GetBOM(ctx, appPool, firmID, ownerID, v2.ID)
	if err != nil {
		t.Fatalf("GetBOM v2: %v", err)
	}
	if v2Reread.IsActive {
		t.Error("expected v2 to have been deactivated when v1 was reactivated")
	}
}

// TestGetBOM_ComponentListIntegrity confirms a BOM's lines round-trip
// intact: every requested component, its quantity, and its unit are
// exactly what GetBOM returns back.
func TestGetBOM_ComponentListIntegrity(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "bom-lines-owner")
	productID := seedProduct(ctx, t, appPool, firmID, ownerID, "WIDGET-2", "Widget", "")
	part1 := seedProduct(ctx, t, appPool, firmID, ownerID, "PART-2A", "Part A", "1.00")
	part2 := seedProduct(ctx, t, appPool, firmID, ownerID, "PART-2B", "Part B", "2.00")

	bom, err := manufacturing.CreateBOM(ctx, appPool, firmID, ownerID, manufacturing.CreateBOMInput{
		ProductID: productID, Name: "Widget BOM",
		Lines: []manufacturing.BOMLineInput{
			{ComponentProductID: part1, QuantityPerUnit: "2", Unit: "piece"},
			{ComponentProductID: part2, QuantityPerUnit: "1.5", Unit: "kg"},
		},
	})
	if err != nil {
		t.Fatalf("CreateBOM: %v", err)
	}

	got, err := manufacturing.GetBOM(ctx, appPool, firmID, ownerID, bom.ID)
	if err != nil {
		t.Fatalf("GetBOM: %v", err)
	}
	if len(got.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got.Lines))
	}
	byComponent := make(map[uuid.UUID]manufacturing.BOMLine, 2)
	for _, l := range got.Lines {
		byComponent[l.ComponentProductID] = l
	}
	if l, ok := byComponent[part1]; !ok || l.QuantityPerUnit != "2.0000" || l.Unit != "piece" {
		t.Errorf("expected part1 line quantity 2.0000/piece, got %+v (found=%v)", l, ok)
	}
	if l, ok := byComponent[part2]; !ok || l.QuantityPerUnit != "1.5000" || l.Unit != "kg" {
		t.Errorf("expected part2 line quantity 1.5000/kg, got %+v (found=%v)", l, ok)
	}
}

// TestStartProductionOrder_IssuesMaterialsAndDeductsStock is this batch's
// own required test case: starting a production order deducts each BOM
// component's quantity_per_unit * quantity_planned from stock and records
// a matching material issue.
func TestStartProductionOrder_IssuesMaterialsAndDeductsStock(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "start-owner")
	productID := seedProduct(ctx, t, appPool, firmID, ownerID, "WIDGET-3", "Widget", "")
	componentID := seedProduct(ctx, t, appPool, firmID, ownerID, "PART-3", "Part", "5.00")
	seedStock(ctx, t, appPool, firmID, componentID, "100")

	bom := createBOM(ctx, t, appPool, firmID, ownerID, productID, componentID, "2", nil)

	po, err := manufacturing.CreateProductionOrder(ctx, appPool, firmID, ownerID, manufacturing.CreateProductionOrderInput{
		ProductID: productID, BOMID: bom.ID, QuantityPlanned: "10",
	})
	if err != nil {
		t.Fatalf("CreateProductionOrder: %v", err)
	}

	started, err := manufacturing.StartProductionOrder(ctx, appPool, firmID, ownerID, po.ID)
	if err != nil {
		t.Fatalf("StartProductionOrder: %v", err)
	}
	if started.Status != manufacturing.ProductionOrderStatusInProgress {
		t.Errorf("expected status in_progress, got %q", started.Status)
	}
	if started.ActualStart == nil {
		t.Error("expected ActualStart to be set")
	}
	if len(started.MaterialIssues) != 1 || started.MaterialIssues[0].QuantityIssued != "20.0000" {
		t.Fatalf("expected one material issue of 20.0000 (2 * 10), got %+v", started.MaterialIssues)
	}

	// 100 - (2 * 10) = 80.
	if got := stockQuantity(ctx, t, adminPool, firmID, componentID); got != "80.0000" {
		t.Errorf("expected component stock 80.0000 after issue, got %q", got)
	}

	var movementCount int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM stock_movements WHERE firm_id = $1 AND product_id = $2 AND reason = 'production_issue'
	`, firmID, componentID).Scan(&movementCount); err != nil {
		t.Fatalf("count stock movements: %v", err)
	}
	if movementCount != 1 {
		t.Errorf("expected 1 production_issue stock movement, got %d", movementCount)
	}
}

// TestStartProductionOrder_InsufficientStockRollsBackAtomically confirms
// the material-issue atomicity design: if ANY component's stock is
// insufficient, the whole Start call fails, no component is deducted,
// and the order stays 'planned'.
func TestStartProductionOrder_InsufficientStockRollsBackAtomically(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "insufficient-owner")
	productID := seedProduct(ctx, t, appPool, firmID, ownerID, "WIDGET-4", "Widget", "")
	plentifulComponent := seedProduct(ctx, t, appPool, firmID, ownerID, "PART-4A", "Plentiful Part", "1.00")
	scarceComponent := seedProduct(ctx, t, appPool, firmID, ownerID, "PART-4B", "Scarce Part", "1.00")
	seedStock(ctx, t, appPool, firmID, plentifulComponent, "1000")
	seedStock(ctx, t, appPool, firmID, scarceComponent, "1")

	bom, err := manufacturing.CreateBOM(ctx, appPool, firmID, ownerID, manufacturing.CreateBOMInput{
		ProductID: productID, Name: "Widget BOM",
		Lines: []manufacturing.BOMLineInput{
			{ComponentProductID: plentifulComponent, QuantityPerUnit: "1"},
			{ComponentProductID: scarceComponent, QuantityPerUnit: "5"},
		},
	})
	if err != nil {
		t.Fatalf("CreateBOM: %v", err)
	}

	po, err := manufacturing.CreateProductionOrder(ctx, appPool, firmID, ownerID, manufacturing.CreateProductionOrderInput{
		ProductID: productID, BOMID: bom.ID, QuantityPlanned: "10",
	})
	if err != nil {
		t.Fatalf("CreateProductionOrder: %v", err)
	}

	if _, err := manufacturing.StartProductionOrder(ctx, appPool, firmID, ownerID, po.ID); !errors.Is(err, inventory.ErrInsufficientStock) {
		t.Fatalf("expected ErrInsufficientStock, got %v", err)
	}

	// Neither component's stock should have moved - the plentiful one's
	// deduction (which would have succeeded on its own) must have rolled
	// back along with the scarce one's rejection.
	if got := stockQuantity(ctx, t, adminPool, firmID, plentifulComponent); got != "1000.0000" {
		t.Errorf("expected plentiful component stock unchanged at 1000.0000, got %q", got)
	}
	if got := stockQuantity(ctx, t, adminPool, firmID, scarceComponent); got != "1.0000" {
		t.Errorf("expected scarce component stock unchanged at 1.0000, got %q", got)
	}

	unchanged, err := manufacturing.GetProductionOrder(ctx, appPool, firmID, ownerID, po.ID)
	if err != nil {
		t.Fatalf("GetProductionOrder: %v", err)
	}
	if unchanged.Status != manufacturing.ProductionOrderStatusPlanned {
		t.Errorf("expected order to remain planned, got %q", unchanged.Status)
	}
	if len(unchanged.MaterialIssues) != 0 {
		t.Errorf("expected no material issues recorded, got %d", len(unchanged.MaterialIssues))
	}
}

// TestCompleteProductionOrder_AddsFinishedGoodAndPostsBalancedJournalEntries
// is this batch's own required test case: completing a production order
// adds the finished quantity to stock and posts a balanced journal entry
// valuing the transfer at the aggregate cost of the components actually
// issued.
func TestCompleteProductionOrder_AddsFinishedGoodAndPostsBalancedJournalEntries(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "complete-owner")
	seedManufacturingAccounts(ctx, t, appPool, firmID)
	productID := seedProduct(ctx, t, appPool, firmID, ownerID, "WIDGET-5", "Widget", "")
	componentID := seedProduct(ctx, t, appPool, firmID, ownerID, "PART-5", "Part", "5.00")
	seedStock(ctx, t, appPool, firmID, componentID, "100")

	bom := createBOM(ctx, t, appPool, firmID, ownerID, productID, componentID, "2", nil)
	po, err := manufacturing.CreateProductionOrder(ctx, appPool, firmID, ownerID, manufacturing.CreateProductionOrderInput{
		ProductID: productID, BOMID: bom.ID, QuantityPlanned: "10",
	})
	if err != nil {
		t.Fatalf("CreateProductionOrder: %v", err)
	}
	if _, err := manufacturing.StartProductionOrder(ctx, appPool, firmID, ownerID, po.ID); err != nil {
		t.Fatalf("StartProductionOrder: %v", err)
	}

	completed, err := manufacturing.CompleteProductionOrder(ctx, appPool, firmID, ownerID, po.ID, "10")
	if err != nil {
		t.Fatalf("CompleteProductionOrder: %v", err)
	}
	if completed.Status != manufacturing.ProductionOrderStatusCompleted {
		t.Errorf("expected status completed, got %q", completed.Status)
	}
	if completed.QuantityProduced != "10.0000" {
		t.Errorf("expected quantity produced 10.0000, got %q", completed.QuantityProduced)
	}
	if completed.ActualEnd == nil {
		t.Error("expected ActualEnd to be set")
	}

	// Finished good stock: 10 added.
	if got := stockQuantity(ctx, t, adminPool, firmID, productID); got != "10.0000" {
		t.Errorf("expected finished good stock 10.0000, got %q", got)
	}

	// Component value: 20 issued (2 * 10) * 5.00 cost = 100.0000.
	var entryCount int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE firm_id = $1 AND source_type = 'production_order'`, firmID).Scan(&entryCount); err != nil {
		t.Fatalf("count journal entries: %v", err)
	}
	if entryCount != 1 {
		t.Fatalf("expected exactly one journal entry, got %d", entryCount)
	}

	rows, err := adminPool.Query(ctx, `
		SELECT a.code, jl.side, jl.amount::text
		FROM journal_lines jl
		JOIN journal_entries je ON je.id = jl.entry_id
		JOIN accounts a ON a.id = jl.account_id
		WHERE je.firm_id = $1 AND je.source_type = 'production_order'
		ORDER BY a.code, jl.side
	`, firmID)
	if err != nil {
		t.Fatalf("query journal lines: %v", err)
	}
	defer rows.Close()
	type line struct{ code, side, amount string }
	var lines []line
	var debitTotal, creditTotal float64
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.code, &l.side, &l.amount); err != nil {
			t.Fatalf("scan journal line: %v", err)
		}
		lines = append(lines, l)
		amt, err := strconv.ParseFloat(l.amount, 64)
		if err != nil {
			t.Fatalf("parse amount %q: %v", l.amount, err)
		}
		if l.side == "debit" {
			debitTotal += amt
		} else {
			creditTotal += amt
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate journal lines: %v", err)
	}
	if len(lines) != 4 {
		t.Fatalf("expected 4 journal lines (DR WIP/CR Inventory, DR Finished Goods/CR WIP), got %d: %+v", len(lines), lines)
	}
	if debitTotal != creditTotal {
		t.Errorf("expected a balanced entry, got debit total %v vs credit total %v: %+v", debitTotal, creditTotal, lines)
	}
	for _, l := range lines {
		if l.amount != "100.0000" {
			t.Errorf("expected every line amount 100.0000 (20 issued * 5.00 cost), got %+v", l)
		}
	}
}

// TestCancelProductionOrder_LeavesStockUnchanged is this batch's own
// required test case: cancelling a planned production order (which never
// issued any materials) leaves stock exactly as it was.
func TestCancelProductionOrder_LeavesStockUnchanged(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "cancel-owner")
	productID := seedProduct(ctx, t, appPool, firmID, ownerID, "WIDGET-6", "Widget", "")
	componentID := seedProduct(ctx, t, appPool, firmID, ownerID, "PART-6", "Part", "1.00")
	seedStock(ctx, t, appPool, firmID, componentID, "50")

	bom := createBOM(ctx, t, appPool, firmID, ownerID, productID, componentID, "2", nil)
	po, err := manufacturing.CreateProductionOrder(ctx, appPool, firmID, ownerID, manufacturing.CreateProductionOrderInput{
		ProductID: productID, BOMID: bom.ID, QuantityPlanned: "10",
	})
	if err != nil {
		t.Fatalf("CreateProductionOrder: %v", err)
	}

	cancelled, err := manufacturing.CancelProductionOrder(ctx, appPool, firmID, ownerID, po.ID)
	if err != nil {
		t.Fatalf("CancelProductionOrder: %v", err)
	}
	if cancelled.Status != manufacturing.ProductionOrderStatusCancelled {
		t.Errorf("expected status cancelled, got %q", cancelled.Status)
	}

	if got := stockQuantity(ctx, t, adminPool, firmID, componentID); got != "50.0000" {
		t.Errorf("expected component stock unchanged at 50.0000, got %q", got)
	}

	if _, err := manufacturing.StartProductionOrder(ctx, appPool, firmID, ownerID, po.ID); !errors.Is(err, manufacturing.ErrInvalidProductionOrderTransition) {
		t.Errorf("expected starting a cancelled order to be rejected, got %v", err)
	}
}
