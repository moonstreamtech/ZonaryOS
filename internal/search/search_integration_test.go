// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package search_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/crm"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/search"
)

// These tests exercise cross-entity search against a real Postgres
// instance - RLS isolation and permission enforcement can't be
// meaningfully faked, same convention as every other package's
// integration tests in this codebase. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func testDSNs(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run search integration tests")
	}
	return adminDSN, appDSN
}

func setupTest(t *testing.T) (adminPool, appPool *pgxpool.Pool) {
	t.Helper()
	adminDSN, appDSN := testDSNs(t)
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
			products, customers, people, suppliers, audit_log, permissions CASCADE
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
	return firmID, userID
}

// TestSearch_ReturnsHitsFromMultipleTypes is Part 1's own required test
// case: cross-entity search returns hits from multiple types in one call.
func TestSearch_ReturnsHitsFromMultipleTypes(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm Cross Search", "search-owner")

	if _, err := inventory.CreateProduct(ctx, appPool, firmID, ownerID, inventory.CreateProductInput{
		SKU: "AC-1", Name: "Acme Widget",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if _, err := crm.CreateCustomer(ctx, appPool, firmID, ownerID, crm.CreateCustomerInput{
		Name: "Acme Corp", Email: "contact@acme.example",
	}); err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}
	if _, err := inventory.CreateSupplier(ctx, appPool, firmID, ownerID, inventory.CreateSupplierInput{
		Name: "Globex Supplies",
	}); err != nil {
		t.Fatalf("CreateSupplier: %v", err)
	}

	hits, err := search.Search(ctx, appPool, firmID, ownerID, "Acme", search.AllTypes)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits (one product, one customer) matching 'Acme', got %+v", hits)
	}

	seenTypes := map[string]bool{}
	for _, h := range hits {
		seenTypes[h.Type] = true
	}
	if !seenTypes["products"] || !seenTypes["customers"] {
		t.Fatalf("expected hits from both products and customers, got types %v", seenTypes)
	}
	if seenTypes["suppliers"] {
		t.Fatalf("expected no supplier hit for query 'Acme', got %+v", hits)
	}
}

// TestSearch_EmptyQueryReturnsNoHits confirms cross-entity search has no
// "browse everything" mode - an empty q short-circuits before touching
// the database (see Search's own doc comment).
func TestSearch_EmptyQueryReturnsNoHits(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm Empty Search", "search-empty-owner")

	if _, err := inventory.CreateProduct(ctx, appPool, firmID, ownerID, inventory.CreateProductInput{SKU: "X-1", Name: "Something"}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	hits, err := search.Search(ctx, appPool, firmID, ownerID, "", search.AllTypes)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected no hits for an empty query, got %+v", hits)
	}
}

// TestSearch_RejectsUnknownType confirms the closed types= vocabulary.
func TestSearch_RejectsUnknownType(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm Bad Type", "search-badtype-owner")

	_, err := search.Search(ctx, appPool, firmID, ownerID, "anything", []string{"not-a-real-type"})
	if err == nil {
		t.Fatal("expected an error for an unknown search type")
	}
}
