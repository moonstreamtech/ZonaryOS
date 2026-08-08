// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package inventory_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise the inventory core against a real Postgres
// instance - RLS isolation and permission enforcement can't be
// meaningfully faked, same convention as every other package's
// integration tests in this codebase. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run inventory integration tests")
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
			products, stock_levels, stock_movements, suppliers, audit_log, permissions CASCADE
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

// seedOwner creates a firm with an owner-flagged role and a member
// holding it - mirrors internal/hr's own test fixture shape.
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
	return firmID, userID
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

func TestCreateProduct_OwnerCanCreateAndMemberCanList(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "inv-owner")
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "inv-member")

	product, err := inventory.CreateProduct(ctx, appPool, firmID, ownerID, inventory.CreateProductInput{
		SKU: "WIDGET-1", Name: "Widget", UnitPrice: "9.99", CostPrice: "4.00", MinQuantity: "5",
	})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if product.SKU != "WIDGET-1" || product.Unit != "piece" || product.MinQuantity != "5" {
		t.Errorf("unexpected product: %+v", product)
	}

	// A non-owner member must be denied write access.
	if _, err := inventory.CreateProduct(ctx, appPool, firmID, memberID, inventory.CreateProductInput{
		SKU: "SHOULD-FAIL", Name: "Should Be Denied",
	}); !errors.Is(err, inventory.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a non-owner CreateProduct, got %v", err)
	}

	// A duplicate SKU within the same firm must be rejected.
	if _, err := inventory.CreateProduct(ctx, appPool, firmID, ownerID, inventory.CreateProductInput{
		SKU: "WIDGET-1", Name: "Duplicate SKU",
	}); !errors.Is(err, inventory.ErrProductSKUExists) {
		t.Fatalf("expected ErrProductSKUExists for a duplicate SKU, got %v", err)
	}

	// But a member CAN read the catalog.
	products, err := inventory.ListProducts(ctx, appPool, firmID, memberID, inventory.ListProductsOptions{})
	if err != nil {
		t.Fatalf("ListProducts (member): %v", err)
	}
	if len(products) != 1 || products[0].ID != product.ID {
		t.Fatalf("expected the member to see the one seeded product, got %+v", products)
	}
}

func TestCreateSupplier_OwnerCanCreateAndMemberCanList(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "inv-supplier-owner")
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "inv-supplier-member")

	supplier, err := inventory.CreateSupplier(ctx, appPool, firmID, ownerID, inventory.CreateSupplierInput{
		Name: "Acme Supply Co", Email: "sales@acme.example",
	})
	if err != nil {
		t.Fatalf("CreateSupplier: %v", err)
	}
	if supplier.Name != "Acme Supply Co" || supplier.Currency != "TRY" || !supplier.IsActive {
		t.Errorf("unexpected supplier: %+v", supplier)
	}

	if _, err := inventory.CreateSupplier(ctx, appPool, firmID, memberID, inventory.CreateSupplierInput{
		Name: "Should Be Denied",
	}); !errors.Is(err, inventory.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a non-owner CreateSupplier, got %v", err)
	}

	suppliers, err := inventory.ListSuppliers(ctx, appPool, firmID, memberID, inventory.ListSuppliersOptions{})
	if err != nil {
		t.Fatalf("ListSuppliers (member): %v", err)
	}
	if len(suppliers) != 1 || suppliers[0].ID != supplier.ID {
		t.Fatalf("expected the member to see the one seeded supplier, got %+v", suppliers)
	}

	var exists bool
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		exists, err = inventory.SupplierExistsTx(ctx, tx, firmID, supplier.ID)
		return err
	})
	if err != nil {
		t.Fatalf("SupplierExistsTx: %v", err)
	}
	if !exists {
		t.Errorf("expected SupplierExistsTx to report true for a real supplier")
	}
}

// TestAdjustStockTx_DecreasesAndAppendsMovement confirms AdjustStockTx's
// core write path: a negative quantityChange decreases stock_levels.quantity
// and appends exactly one stock_movements row recording it - direct,
// package-level test of the primitive internal/workflow's record_sale
// hook builds on (see internal/workflow/stock_bridge_integration_test.go's
// own end-to-end test of that hook).
func TestAdjustStockTx_DecreasesAndAppendsMovement(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "inv-adjust-owner")
	product, err := inventory.CreateProduct(ctx, appPool, firmID, ownerID, inventory.CreateProductInput{SKU: "ADJ-1", Name: "Adjustable"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return inventory.AdjustStockTx(ctx, tx, firmID, product.ID, "", "10", "purchase", "test", nil)
	})
	if err != nil {
		t.Fatalf("AdjustStockTx (initial increase): %v", err)
	}

	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return inventory.AdjustStockTx(ctx, tx, firmID, product.ID, "", "-4", "sale", "test", nil)
	})
	if err != nil {
		t.Fatalf("AdjustStockTx (decrease): %v", err)
	}

	levels, err := inventory.GetStock(ctx, appPool, firmID, ownerID, product.ID)
	if err != nil {
		t.Fatalf("GetStock: %v", err)
	}
	if len(levels) != 1 || levels[0].Quantity != "6.0000" {
		t.Fatalf("expected quantity 6.0000 at one location, got %+v", levels)
	}

	movements, err := inventory.ListStockMovements(ctx, appPool, firmID, ownerID, inventory.ListMovementsOptions{})
	if err != nil {
		t.Fatalf("ListStockMovements: %v", err)
	}
	if movements.Total != 2 {
		t.Fatalf("expected exactly two stock movements, got %+v", movements)
	}
}

// TestAdjustStockTx_RejectsInsufficientStock confirms the design brief's
// "reject, not just warn" requirement at the primitive level: a
// quantityChange that would take quantity below zero returns
// ErrInsufficientStock and leaves stock_levels/stock_movements untouched.
func TestAdjustStockTx_RejectsInsufficientStock(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "inv-reject-owner")
	product, err := inventory.CreateProduct(ctx, appPool, firmID, ownerID, inventory.CreateProductInput{SKU: "ADJ-2", Name: "Scarce"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return inventory.AdjustStockTx(ctx, tx, firmID, product.ID, "", "2", "purchase", "test", nil)
	})
	if err != nil {
		t.Fatalf("AdjustStockTx (initial increase): %v", err)
	}

	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return inventory.AdjustStockTx(ctx, tx, firmID, product.ID, "", "-5", "sale", "test", nil)
	})
	if !errors.Is(err, inventory.ErrInsufficientStock) {
		t.Fatalf("expected ErrInsufficientStock, got %v", err)
	}

	levels, err := inventory.GetStock(ctx, appPool, firmID, ownerID, product.ID)
	if err != nil {
		t.Fatalf("GetStock: %v", err)
	}
	if len(levels) != 1 || levels[0].Quantity != "2.0000" {
		t.Fatalf("expected quantity to remain 2.0000, got %+v", levels)
	}

	movements, err := inventory.ListStockMovements(ctx, appPool, firmID, ownerID, inventory.ListMovementsOptions{})
	if err != nil {
		t.Fatalf("ListStockMovements: %v", err)
	}
	if movements.Total != 1 {
		t.Fatalf("expected only the initial increase's movement to exist, got %+v", movements)
	}
}
