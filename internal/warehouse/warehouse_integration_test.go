// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package warehouse_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/warehouse"
	"github.com/moonstreamtech/ZonaryOS/migrations"
)

// execSQLFile runs each ";"-separated statement in sql individually -
// pgx's pool.Exec uses the extended query protocol by default, which
// (unlike golang-migrate's own driver) can't run a whole multi-statement
// migration file in one call. Safe here because
// migrations/0028_warehouse_management.{up,down}.sql contain only plain
// statements, no semicolons inside string literals or PL/pgSQL blocks.
func execSQLFile(ctx context.Context, t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	for _, stmt := range strings.Split(sql, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("exec statement %q: %v", stmt, err)
		}
	}
}

// These tests exercise internal/warehouse against a real Postgres
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
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run warehouse integration tests")
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
			warehouses, warehouse_locations, inventory_transfers, cycle_count_sessions, cycle_count_lines,
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

// seedOwner creates a firm with an owner-flagged role, a member holding
// it, and a default warehouse location (AdjustStockTx's nil-locationID
// resolution needs one) - mirrors internal/manufacturing's own test
// fixture shape.
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

	var whID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO warehouses (firm_id, name) VALUES ($1, 'Default Warehouse') RETURNING id`, firmID).Scan(&whID); err != nil {
		t.Fatalf("seed default warehouse: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO warehouse_locations (firm_id, warehouse_id, code, name, is_default) VALUES ($1, $2, 'default', 'Main', true)
	`, firmID, whID); err != nil {
		t.Fatalf("seed default location: %v", err)
	}
	return firmID, userID
}

func seedProduct(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID, sku, costPrice string) uuid.UUID {
	t.Helper()
	var productID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO products (firm_id, sku, name, cost_price) VALUES ($1, $2, $2, $3) RETURNING id
	`, firmID, sku, costPrice).Scan(&productID); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	return productID
}

func seedStockAt(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID, productID, locationID uuid.UUID, quantity string) {
	t.Helper()
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return inventory.AdjustStockTx(ctx, tx, firmID, productID, &locationID, quantity, "purchase", "test", nil)
	})
	if err != nil {
		t.Fatalf("seed stock: %v", err)
	}
}

func seedInventoryAccounts(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID uuid.UUID) {
	t.Helper()
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		for _, a := range []struct{ code, name, typ string }{
			{"1200", "Inventory", "asset"},
			{"1500", "Inventory Adjustment", "expense"},
		} {
			if _, err := tx.Exec(ctx, `INSERT INTO accounts (firm_id, code, name, type) VALUES ($1, $2, $3, $4)`, firmID, a.code, a.name, a.typ); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed inventory accounts: %v", err)
	}
}

func defaultLocationID(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT id FROM warehouse_locations WHERE firm_id = $1 AND is_default`, firmID).Scan(&id); err != nil {
		t.Fatalf("look up default location: %v", err)
	}
	return id
}

func defaultWarehouseID(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := adminPool.QueryRow(ctx, `SELECT warehouse_id FROM warehouse_locations WHERE firm_id = $1 AND is_default`, firmID).Scan(&id); err != nil {
		t.Fatalf("look up default warehouse: %v", err)
	}
	return id
}

// stockQuantityAt returns "0" if no stock_levels row exists yet for this
// (firm, product, location) - AdjustStockTx only creates the row on its
// first call, so "never touched" and "touched, currently zero" both need
// to read the same way for a test asserting nothing moved.
func stockQuantityAt(ctx context.Context, t *testing.T, adminPool *pgxpool.Pool, firmID, productID, locationID uuid.UUID) string {
	t.Helper()
	var qty string
	err := adminPool.QueryRow(ctx, `
		SELECT quantity::text FROM stock_levels WHERE firm_id = $1 AND product_id = $2 AND location_id = $3
	`, firmID, productID, locationID).Scan(&qty)
	if errors.Is(err, pgx.ErrNoRows) {
		return "0"
	}
	if err != nil {
		t.Fatalf("read stock quantity: %v", err)
	}
	return qty
}

// ---------------------------------------------------------------------
// Warehouses/locations CRUD
// ---------------------------------------------------------------------

func TestCreateWarehouseAndLocation_HierarchyAndOwnerGating(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "wh-owner")

	wh, err := warehouse.CreateWarehouse(ctx, appPool, firmID, ownerID, warehouse.CreateWarehouseInput{Name: "North Depot", Address: "1 Depot Rd"})
	if err != nil {
		t.Fatalf("CreateWarehouse: %v", err)
	}

	aisle, err := warehouse.CreateLocation(ctx, appPool, firmID, ownerID, wh.ID, warehouse.CreateLocationInput{Code: "A", Type: "aisle"})
	if err != nil {
		t.Fatalf("CreateLocation (aisle): %v", err)
	}
	shelf, err := warehouse.CreateLocation(ctx, appPool, firmID, ownerID, wh.ID, warehouse.CreateLocationInput{Code: "A-01", Type: "shelf", ParentID: &aisle.ID})
	if err != nil {
		t.Fatalf("CreateLocation (shelf): %v", err)
	}
	bin, err := warehouse.CreateLocation(ctx, appPool, firmID, ownerID, wh.ID, warehouse.CreateLocationInput{Code: "A-01-03", Type: "bin", ParentID: &shelf.ID})
	if err != nil {
		t.Fatalf("CreateLocation (bin): %v", err)
	}
	if bin.ParentID == nil || *bin.ParentID != shelf.ID {
		t.Errorf("expected bin's parent to be the shelf, got %v", bin.ParentID)
	}

	// A parent from a DIFFERENT warehouse must be rejected.
	otherWh, err := warehouse.CreateWarehouse(ctx, appPool, firmID, ownerID, warehouse.CreateWarehouseInput{Name: "South Depot"})
	if err != nil {
		t.Fatalf("CreateWarehouse (other): %v", err)
	}
	if _, err := warehouse.CreateLocation(ctx, appPool, firmID, ownerID, otherWh.ID, warehouse.CreateLocationInput{Code: "X", ParentID: &aisle.ID}); !errors.Is(err, warehouse.ErrInvalidLocation) {
		t.Fatalf("expected ErrInvalidLocation for a cross-warehouse parent, got %v", err)
	}

	locations, err := warehouse.ListLocations(ctx, appPool, firmID, ownerID, wh.ID)
	if err != nil {
		t.Fatalf("ListLocations: %v", err)
	}
	if len(locations) != 3 {
		t.Fatalf("expected 3 locations, got %d", len(locations))
	}

	// Member (non-owner) can read but not write.
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "wh-member")
	if _, err := warehouse.ListWarehouses(ctx, appPool, firmID, memberID); err != nil {
		t.Errorf("member ListWarehouses: %v", err)
	}
	if _, err := warehouse.CreateWarehouse(ctx, appPool, firmID, memberID, warehouse.CreateWarehouseInput{Name: "Nope"}); !errors.Is(err, warehouse.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a member's CreateWarehouse, got %v", err)
	}
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

// ---------------------------------------------------------------------
// Inventory transfers
// ---------------------------------------------------------------------

func TestCompleteTransfer_AtomicDeductionAndAddition(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "transfer-owner")
	fromLocationID := defaultLocationID(ctx, t, adminPool, firmID)
	whID := defaultWarehouseID(ctx, t, adminPool, firmID)
	toLocation, err := warehouse.CreateLocation(ctx, appPool, firmID, ownerID, whID, warehouse.CreateLocationInput{Code: "B"})
	if err != nil {
		t.Fatalf("CreateLocation: %v", err)
	}

	productID := seedProduct(ctx, t, adminPool, firmID, "WIDGET-T1", "5.00")
	seedStockAt(ctx, t, appPool, firmID, productID, fromLocationID, "10")

	transfer, err := warehouse.CreateTransfer(ctx, appPool, firmID, ownerID, warehouse.CreateTransferInput{
		FromLocationID: fromLocationID, ToLocationID: toLocation.ID, ProductID: productID, Quantity: "4",
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	if transfer.Status != warehouse.TransferStatusDraft {
		t.Fatalf("expected new transfer status draft, got %q", transfer.Status)
	}

	completed, err := warehouse.CompleteTransfer(ctx, appPool, firmID, ownerID, transfer.ID)
	if err != nil {
		t.Fatalf("CompleteTransfer: %v", err)
	}
	if completed.Status != warehouse.TransferStatusCompleted {
		t.Fatalf("expected status completed, got %q", completed.Status)
	}

	if got := stockQuantityAt(ctx, t, adminPool, firmID, productID, fromLocationID); got != "6.0000" {
		t.Errorf("expected from-location quantity 6.0000, got %q", got)
	}
	if got := stockQuantityAt(ctx, t, adminPool, firmID, productID, toLocation.ID); got != "4.0000" {
		t.Errorf("expected to-location quantity 4.0000, got %q", got)
	}

	var outReason, inReason string
	if err := adminPool.QueryRow(ctx, `
		SELECT reason FROM stock_movements WHERE location_id = $1 AND product_id = $2 ORDER BY created_at DESC LIMIT 1
	`, fromLocationID, productID).Scan(&outReason); err != nil {
		t.Fatalf("read transfer_out movement: %v", err)
	}
	if outReason != "transfer_out" {
		t.Errorf("expected reason transfer_out, got %q", outReason)
	}
	if err := adminPool.QueryRow(ctx, `
		SELECT reason FROM stock_movements WHERE location_id = $1 AND product_id = $2 ORDER BY created_at DESC LIMIT 1
	`, toLocation.ID, productID).Scan(&inReason); err != nil {
		t.Fatalf("read transfer_in movement: %v", err)
	}
	if inReason != "transfer_in" {
		t.Errorf("expected reason transfer_in, got %q", inReason)
	}
}

func TestCompleteTransfer_RejectsInsufficientStockAndLeavesStatusUnchanged(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "transfer-insufficient-owner")
	fromLocationID := defaultLocationID(ctx, t, adminPool, firmID)
	whID := defaultWarehouseID(ctx, t, adminPool, firmID)
	toLocation, err := warehouse.CreateLocation(ctx, appPool, firmID, ownerID, whID, warehouse.CreateLocationInput{Code: "B"})
	if err != nil {
		t.Fatalf("CreateLocation: %v", err)
	}

	productID := seedProduct(ctx, t, adminPool, firmID, "WIDGET-T2", "5.00")
	seedStockAt(ctx, t, appPool, firmID, productID, fromLocationID, "2")

	transfer, err := warehouse.CreateTransfer(ctx, appPool, firmID, ownerID, warehouse.CreateTransferInput{
		FromLocationID: fromLocationID, ToLocationID: toLocation.ID, ProductID: productID, Quantity: "5",
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}

	_, err = warehouse.CompleteTransfer(ctx, appPool, firmID, ownerID, transfer.ID)
	if !errors.Is(err, inventory.ErrInsufficientStock) {
		t.Fatalf("expected ErrInsufficientStock, got %v", err)
	}

	if got := stockQuantityAt(ctx, t, adminPool, firmID, productID, fromLocationID); got != "2.0000" {
		t.Errorf("expected from-location quantity unchanged at 2.0000, got %q", got)
	}
	if got := stockQuantityAt(ctx, t, adminPool, firmID, productID, toLocation.ID); got != "0" {
		t.Errorf("expected to-location to have received nothing, got %q", got)
	}

	after, err := warehouse.GetTransfer(ctx, appPool, firmID, ownerID, transfer.ID)
	if err != nil {
		t.Fatalf("GetTransfer: %v", err)
	}
	if after.Status != warehouse.TransferStatusDraft {
		t.Errorf("expected the transfer to remain draft after a rejected completion, got %q", after.Status)
	}
}

func TestCancelTransfer_LeavesStockUnchanged(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "transfer-cancel-owner")
	fromLocationID := defaultLocationID(ctx, t, adminPool, firmID)
	whID := defaultWarehouseID(ctx, t, adminPool, firmID)
	toLocation, err := warehouse.CreateLocation(ctx, appPool, firmID, ownerID, whID, warehouse.CreateLocationInput{Code: "B"})
	if err != nil {
		t.Fatalf("CreateLocation: %v", err)
	}
	productID := seedProduct(ctx, t, adminPool, firmID, "WIDGET-T3", "5.00")
	seedStockAt(ctx, t, appPool, firmID, productID, fromLocationID, "10")

	transfer, err := warehouse.CreateTransfer(ctx, appPool, firmID, ownerID, warehouse.CreateTransferInput{
		FromLocationID: fromLocationID, ToLocationID: toLocation.ID, ProductID: productID, Quantity: "4",
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}

	cancelled, err := warehouse.CancelTransfer(ctx, appPool, firmID, ownerID, transfer.ID)
	if err != nil {
		t.Fatalf("CancelTransfer: %v", err)
	}
	if cancelled.Status != warehouse.TransferStatusCancelled {
		t.Fatalf("expected status cancelled, got %q", cancelled.Status)
	}
	if got := stockQuantityAt(ctx, t, adminPool, firmID, productID, fromLocationID); got != "10.0000" {
		t.Errorf("expected from-location quantity unchanged at 10.0000, got %q", got)
	}

	if _, err := warehouse.CompleteTransfer(ctx, appPool, firmID, ownerID, transfer.ID); !errors.Is(err, warehouse.ErrInvalidTransferTransition) {
		t.Fatalf("expected ErrInvalidTransferTransition completing a cancelled transfer, got %v", err)
	}
}

// ---------------------------------------------------------------------
// Cycle counting
// ---------------------------------------------------------------------

func TestCompleteCycleCountSession_ComputesVarianceAndPostsBalancedJournalEntry(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "cyclecount-owner")
	seedInventoryAccounts(ctx, t, appPool, firmID)
	locationID := defaultLocationID(ctx, t, adminPool, firmID)
	whID := defaultWarehouseID(ctx, t, adminPool, firmID)

	// shortProductID: system says 10, count finds 7 - shrinkage (negative
	// variance), valued at cost_price 5.00 -> -15.00.
	shortProductID := seedProduct(ctx, t, adminPool, firmID, "WIDGET-CC1", "5.00")
	seedStockAt(ctx, t, appPool, firmID, shortProductID, locationID, "10")
	// surplusProductID: system says 3, count finds 5 - a found surplus
	// (positive variance), valued at cost_price 2.00 -> +4.00.
	surplusProductID := seedProduct(ctx, t, adminPool, firmID, "WIDGET-CC2", "2.00")
	seedStockAt(ctx, t, appPool, firmID, surplusProductID, locationID, "3")

	// Net value across the session: -15.00 + 4.00 = -11.00 (net
	// shrinkage) - DR Inventory Adjustment / CR Inventory, 11.00.
	session, err := warehouse.CreateCycleCountSession(ctx, appPool, firmID, ownerID, warehouse.CreateCycleCountSessionInput{
		WarehouseID: whID,
		Lines: []warehouse.CycleCountLineInput{
			{LocationID: locationID, ProductID: shortProductID},
			{LocationID: locationID, ProductID: surplusProductID},
		},
	})
	if err != nil {
		t.Fatalf("CreateCycleCountSession: %v", err)
	}
	if len(session.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(session.Lines))
	}
	if session.Lines[0].SystemQuantity != "10.0000" {
		t.Errorf("expected first line system quantity 10.0000, got %q", session.Lines[0].SystemQuantity)
	}

	var shortLineID, surplusLineID uuid.UUID
	for _, l := range session.Lines {
		switch l.ProductID {
		case shortProductID:
			shortLineID = l.ID
		case surplusProductID:
			surplusLineID = l.ID
		}
	}

	if _, err := warehouse.RecordCount(ctx, appPool, firmID, ownerID, session.ID, shortLineID, "7"); err != nil {
		t.Fatalf("RecordCount (short): %v", err)
	}
	countedSurplus, err := warehouse.RecordCount(ctx, appPool, firmID, ownerID, session.ID, surplusLineID, "5")
	if err != nil {
		t.Fatalf("RecordCount (surplus): %v", err)
	}
	if countedSurplus.Variance == nil || *countedSurplus.Variance != "2.0000" {
		t.Fatalf("expected variance 2.0000, got %v", countedSurplus.Variance)
	}

	completed, err := warehouse.CompleteCycleCountSession(ctx, appPool, firmID, ownerID, session.ID, true)
	if err != nil {
		t.Fatalf("CompleteCycleCountSession: %v", err)
	}
	if completed.Status != warehouse.CycleCountSessionStatusCompleted {
		t.Fatalf("expected status completed, got %q", completed.Status)
	}
	for _, l := range completed.Lines {
		if !l.Adjusted {
			t.Errorf("expected line %s to be flagged adjusted, was not", l.ID)
		}
	}

	if got := stockQuantityAt(ctx, t, adminPool, firmID, shortProductID, locationID); got != "7.0000" {
		t.Errorf("expected short product stock adjusted to 7.0000, got %q", got)
	}
	if got := stockQuantityAt(ctx, t, adminPool, firmID, surplusProductID, locationID); got != "5.0000" {
		t.Errorf("expected surplus product stock adjusted to 5.0000, got %q", got)
	}

	var shortReason, surplusReason string
	if err := adminPool.QueryRow(ctx, `SELECT reason FROM stock_movements WHERE product_id = $1 AND location_id = $2 ORDER BY created_at DESC LIMIT 1`, shortProductID, locationID).Scan(&shortReason); err != nil {
		t.Fatalf("read short movement: %v", err)
	}
	if shortReason != "cycle_count" {
		t.Errorf("expected reason cycle_count, got %q", shortReason)
	}
	if err := adminPool.QueryRow(ctx, `SELECT reason FROM stock_movements WHERE product_id = $1 AND location_id = $2 ORDER BY created_at DESC LIMIT 1`, surplusProductID, locationID).Scan(&surplusReason); err != nil {
		t.Fatalf("read surplus movement: %v", err)
	}
	if surplusReason != "cycle_count" {
		t.Errorf("expected reason cycle_count, got %q", surplusReason)
	}

	// One balanced journal entry, net shrinkage: DR Inventory Adjustment
	// 11.00 / CR Inventory 11.00.
	var entryCount int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE firm_id = $1`, firmID).Scan(&entryCount); err != nil {
		t.Fatalf("count journal entries: %v", err)
	}
	if entryCount != 1 {
		t.Fatalf("expected exactly one journal entry, got %d", entryCount)
	}

	rows, err := adminPool.Query(ctx, `
		SELECT a.code, jl.side, jl.amount::text
		FROM journal_lines jl
		JOIN accounts a ON a.id = jl.account_id
		JOIN journal_entries je ON je.id = jl.entry_id
		WHERE je.firm_id = $1
		ORDER BY a.code
	`, firmID)
	if err != nil {
		t.Fatalf("read journal lines: %v", err)
	}
	defer rows.Close()
	type line struct{ code, side, amount string }
	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.code, &l.side, &l.amount); err != nil {
			t.Fatalf("scan journal line: %v", err)
		}
		lines = append(lines, l)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 journal lines, got %d", len(lines))
	}
	if lines[0].code != "1200" || lines[0].side != "credit" || lines[0].amount != "11.0000" {
		t.Errorf("expected CR Inventory 11.0000, got %+v", lines[0])
	}
	if lines[1].code != "1500" || lines[1].side != "debit" || lines[1].amount != "11.0000" {
		t.Errorf("expected DR Inventory Adjustment 11.0000, got %+v", lines[1])
	}
}

func TestCompleteCycleCountSession_ZeroVarianceSkipsAdjustmentAndJournal(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "cyclecount-zero-owner")
	seedInventoryAccounts(ctx, t, appPool, firmID)
	locationID := defaultLocationID(ctx, t, adminPool, firmID)
	whID := defaultWarehouseID(ctx, t, adminPool, firmID)

	productID := seedProduct(ctx, t, adminPool, firmID, "WIDGET-CC3", "5.00")
	seedStockAt(ctx, t, appPool, firmID, productID, locationID, "10")

	session, err := warehouse.CreateCycleCountSession(ctx, appPool, firmID, ownerID, warehouse.CreateCycleCountSessionInput{
		WarehouseID: whID,
		Lines:       []warehouse.CycleCountLineInput{{LocationID: locationID, ProductID: productID}},
	})
	if err != nil {
		t.Fatalf("CreateCycleCountSession: %v", err)
	}
	if _, err := warehouse.RecordCount(ctx, appPool, firmID, ownerID, session.ID, session.Lines[0].ID, "10"); err != nil {
		t.Fatalf("RecordCount: %v", err)
	}

	completed, err := warehouse.CompleteCycleCountSession(ctx, appPool, firmID, ownerID, session.ID, true)
	if err != nil {
		t.Fatalf("CompleteCycleCountSession: %v", err)
	}
	if completed.Lines[0].Adjusted {
		t.Errorf("expected a zero-variance line to NOT be flagged adjusted")
	}

	var entryCount int
	if err := adminPool.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE firm_id = $1`, firmID).Scan(&entryCount); err != nil {
		t.Fatalf("count journal entries: %v", err)
	}
	if entryCount != 0 {
		t.Errorf("expected no journal entry for a zero-net-variance session, got %d", entryCount)
	}
	if got := stockQuantityAt(ctx, t, adminPool, firmID, productID, locationID); got != "10.0000" {
		t.Errorf("expected stock unchanged at 10.0000, got %q", got)
	}
}

// ---------------------------------------------------------------------
// Migration 0028 backfill
// ---------------------------------------------------------------------

// TestMigration0028Backfill_PreservesExistingStockLevels replays the
// exact scenario the migration itself had to handle: an existing
// deployment with stock_levels rows already keyed by the OLD free-text
// `location` column before location_id existed. It runs migration
// 0028's own down.sql (dropping back to the free-text column, on a
// database that already has every OTHER migration applied), seeds a
// firm/product/stock_levels row the old way, re-runs 0028's up.sql, and
// asserts the row survived with a resolvable location_id pointing at a
// real warehouse_locations row whose code matches the old text value -
// exactly what migrations/0028_warehouse_management.up.sql's own doc
// comment promises ("no data loss, existing deployments won't break
// mid-migration").
func TestMigration0028Backfill_PreservesExistingStockLevels(t *testing.T) {
	adminPool, _ := setupTest(t)
	ctx := context.Background()

	downSQL, err := migrations.FS.ReadFile("0028_warehouse_management.down.sql")
	if err != nil {
		t.Fatalf("read 0028 down.sql: %v", err)
	}
	upSQL, err := migrations.FS.ReadFile("0028_warehouse_management.up.sql")
	if err != nil {
		t.Fatalf("read 0028 up.sql: %v", err)
	}

	execSQLFile(ctx, t, adminPool, string(downSQL))

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Pre-Migration Firm') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed pre-migration firm: %v", err)
	}
	var productID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO products (firm_id, sku, name) VALUES ($1, 'PRE-SKU', 'Pre-migration product') RETURNING id`, firmID).Scan(&productID); err != nil {
		t.Fatalf("seed pre-migration product: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO stock_levels (firm_id, product_id, location, quantity) VALUES ($1, $2, 'default', 42)
	`, firmID, productID); err != nil {
		t.Fatalf("seed pre-migration stock_levels row: %v", err)
	}
	// A second, non-default location text value, exercising the
	// "any other distinct location text also gets its own row" branch of
	// the backfill.
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO stock_levels (firm_id, product_id, location, quantity) VALUES ($1, $2, 'overflow-bin', 8)
	`, firmID, productID); err != nil {
		t.Fatalf("seed second pre-migration stock_levels row: %v", err)
	}

	execSQLFile(ctx, t, adminPool, string(upSQL))

	var defaultQty, overflowQty string
	var defaultCode, overflowCode string
	var isDefault bool
	if err := adminPool.QueryRow(ctx, `
		SELECT sl.quantity::text, wl.code, wl.is_default
		FROM stock_levels sl JOIN warehouse_locations wl ON wl.id = sl.location_id
		WHERE sl.firm_id = $1 AND sl.product_id = $2 AND wl.code = 'default'
	`, firmID, productID).Scan(&defaultQty, &defaultCode, &isDefault); err != nil {
		t.Fatalf("read backfilled default-location row: %v", err)
	}
	if defaultQty != "42.0000" {
		t.Errorf("expected preserved quantity 42.0000 at the default location, got %q", defaultQty)
	}
	if !isDefault {
		t.Errorf("expected the 'default'-coded location to be flagged is_default")
	}

	if err := adminPool.QueryRow(ctx, `
		SELECT sl.quantity::text, wl.code
		FROM stock_levels sl JOIN warehouse_locations wl ON wl.id = sl.location_id
		WHERE sl.firm_id = $1 AND sl.product_id = $2 AND wl.code = 'overflow-bin'
	`, firmID, productID).Scan(&overflowQty, &overflowCode); err != nil {
		t.Fatalf("read backfilled overflow-bin row: %v", err)
	}
	if overflowQty != "8.0000" {
		t.Errorf("expected preserved quantity 8.0000 at overflow-bin, got %q", overflowQty)
	}

	// Every firm - not just this one with prior stock - gets a default
	// location unconditionally (AdjustStockTx's nil-locationID resolution
	// needs one for ANY firm).
	var anyFirmsWithoutDefault int
	if err := adminPool.QueryRow(ctx, `
		SELECT count(*) FROM firms f
		WHERE NOT EXISTS (SELECT 1 FROM warehouse_locations wl WHERE wl.firm_id = f.id AND wl.is_default)
	`).Scan(&anyFirmsWithoutDefault); err != nil {
		t.Fatalf("count firms without a default location: %v", err)
	}
	if anyFirmsWithoutDefault != 0 {
		t.Errorf("expected every firm to have a default warehouse location after the backfill, %d do not", anyFirmsWithoutDefault)
	}
}
