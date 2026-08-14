// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package salesorders_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/salesorders"
)

// These tests exercise internal/salesorders against a real Postgres
// instance - RLS isolation and permission enforcement can't be
// meaningfully faked, same convention every other package's integration
// tests in this codebase follow (see internal/invoicing's own tests).
// Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL and
// ZONARYOS_TEST_APP_DATABASE_URL are set.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run sales order integration tests")
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
			sales_orders, sales_order_lines, sales_order_sequences, customers, audit_log, permissions CASCADE
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

func oneLine() []salesorders.SalesOrderLineInput {
	return []salesorders.SalesOrderLineInput{{Description: "Widget", Quantity: "1", UnitPrice: "10.00"}}
}

// TestCreateSalesOrder_SequentialOrderNumbers mirrors
// internal/invoicing's own TestCreateInvoice_SequentialInvoiceNumbers:
// two orders for the same firm get SO-0001 then SO-0002.
func TestCreateSalesOrder_SequentialOrderNumbers(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "so-seq-owner")

	first, err := salesorders.CreateSalesOrder(ctx, appPool, firmID, ownerID, nil, "", "", "", oneLine())
	if err != nil {
		t.Fatalf("CreateSalesOrder (first): %v", err)
	}
	second, err := salesorders.CreateSalesOrder(ctx, appPool, firmID, ownerID, nil, "", "", "", oneLine())
	if err != nil {
		t.Fatalf("CreateSalesOrder (second): %v", err)
	}
	if first.OrderNumber != "SO-0001" || second.OrderNumber != "SO-0002" {
		t.Fatalf("expected SO-0001 then SO-0002, got %q then %q", first.OrderNumber, second.OrderNumber)
	}
}

// TestNextOrderNumberTx_ConcurrentCallsGetDistinctNumbers is the
// concurrency proof for nextOrderNumberTx's own doc comment - mirrors
// internal/invoicing.TestNextInvoiceNumberTx_ConcurrentCallsGetDistinctNumbers.
func TestNextOrderNumberTx_ConcurrentCallsGetDistinctNumbers(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "so-concurrent-owner")

	const n = 10
	var wg sync.WaitGroup
	numbers := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			so, err := salesorders.CreateSalesOrder(ctx, appPool, firmID, ownerID, nil, "", "", "", []salesorders.SalesOrderLineInput{
				{Description: fmt.Sprintf("Concurrent %d", i), Quantity: "1", UnitPrice: "1.00"},
			})
			numbers[i] = so.OrderNumber
			errs[i] = err
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent CreateSalesOrder %d: %v", i, err)
		}
		if seen[numbers[i]] {
			t.Fatalf("order number %q was allocated more than once: %v", numbers[i], numbers)
		}
		seen[numbers[i]] = true
	}
	if len(seen) != n {
		t.Fatalf("expected %d distinct order numbers, got %d: %v", n, len(seen), numbers)
	}
}

// TestUpdateSalesOrderStatus_ValidatesTransitions confirms
// allowedStatusTransitions is actually enforced: draft -> confirmed
// succeeds, draft -> shipped (skipping steps) is rejected.
func TestUpdateSalesOrderStatus_ValidatesTransitions(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "so-status-owner")

	so, err := salesorders.CreateSalesOrder(ctx, appPool, firmID, ownerID, nil, "", "", "", oneLine())
	if err != nil {
		t.Fatalf("CreateSalesOrder: %v", err)
	}
	if so.Status != salesorders.SalesOrderStatusDraft {
		t.Fatalf("expected a manually-created order to start draft, got %q", so.Status)
	}

	if _, err := salesorders.UpdateSalesOrderStatus(ctx, appPool, firmID, ownerID, so.ID, salesorders.SalesOrderStatusShipped); err == nil {
		t.Fatalf("expected draft -> shipped to be rejected (skips confirmed/picking)")
	} else if !errors.Is(err, salesorders.ErrInvalidSalesOrder) {
		t.Fatalf("expected ErrInvalidSalesOrder, got %v", err)
	}

	updated, err := salesorders.UpdateSalesOrderStatus(ctx, appPool, firmID, ownerID, so.ID, salesorders.SalesOrderStatusConfirmed)
	if err != nil {
		t.Fatalf("draft -> confirmed: %v", err)
	}
	if updated.Status != salesorders.SalesOrderStatusConfirmed {
		t.Fatalf("expected status confirmed, got %q", updated.Status)
	}
}

// TestGetSalesOrder_NotFoundForOtherFirm confirms RLS isolation: a sales
// order created in one firm is invisible (ErrSalesOrderNotFound, not a
// silent empty read) to a caller scoped to a different firm.
func TestGetSalesOrder_NotFoundForOtherFirm(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmA, ownerA := seedOwner(ctx, t, adminPool, appPool, "Firm A", "so-rls-a")
	firmB, ownerB := seedOwner(ctx, t, adminPool, appPool, "Firm B", "so-rls-b")

	so, err := salesorders.CreateSalesOrder(ctx, appPool, firmA, ownerA, nil, "", "", "", oneLine())
	if err != nil {
		t.Fatalf("CreateSalesOrder: %v", err)
	}

	if _, err := salesorders.GetSalesOrder(ctx, appPool, firmB, ownerB, so.ID); !errors.Is(err, salesorders.ErrSalesOrderNotFound) {
		t.Fatalf("expected ErrSalesOrderNotFound reading firm A's order as firm B, got %v", err)
	}
}
