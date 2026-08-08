// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package logistics_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/logistics"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise the logistics core against a real Postgres
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
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run logistics integration tests")
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
			deliveries, audit_log, permissions CASCADE
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

func TestCreateDelivery_OwnerCanCreateAndMemberCanList(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "log-owner")
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "log-member")

	delivery, err := logistics.CreateDelivery(ctx, appPool, firmID, ownerID, logistics.CreateDeliveryInput{
		DestinationAddress: "123 Main St", Carrier: "Acme Shipping",
	})
	if err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	if delivery.Status != logistics.DeliveryStatusPending {
		t.Errorf("expected a new delivery to start pending, got %q", delivery.Status)
	}

	if _, err := logistics.CreateDelivery(ctx, appPool, firmID, memberID, logistics.CreateDeliveryInput{
		DestinationAddress: "Should Be Denied",
	}); !errors.Is(err, logistics.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a non-owner CreateDelivery, got %v", err)
	}

	deliveries, err := logistics.ListDeliveries(ctx, appPool, firmID, memberID, logistics.ListDeliveriesOptions{})
	if err != nil {
		t.Fatalf("ListDeliveries (member): %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].ID != delivery.ID {
		t.Fatalf("expected the member to see the one seeded delivery, got %+v", deliveries)
	}
}

func TestUpdateDelivery_StatusTransitionAndFilter(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "log-owner-2")

	delivery, err := logistics.CreateDelivery(ctx, appPool, firmID, ownerID, logistics.CreateDeliveryInput{
		DestinationAddress: "456 Oak Ave",
	})
	if err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}

	inTransit := logistics.DeliveryStatusInTransit
	updated, err := logistics.UpdateDelivery(ctx, appPool, firmID, ownerID, delivery.ID, logistics.DeliveryUpdate{Status: &inTransit})
	if err != nil {
		t.Fatalf("UpdateDelivery: %v", err)
	}
	if updated.Status != logistics.DeliveryStatusInTransit {
		t.Errorf("expected status in_transit, got %q", updated.Status)
	}

	pendingOnly, err := logistics.ListDeliveries(ctx, appPool, firmID, ownerID, logistics.ListDeliveriesOptions{Status: logistics.DeliveryStatusPending})
	if err != nil {
		t.Fatalf("ListDeliveries (pending filter): %v", err)
	}
	if len(pendingOnly) != 0 {
		t.Errorf("expected no pending deliveries after the status change, got %+v", pendingOnly)
	}

	inTransitOnly, err := logistics.ListDeliveries(ctx, appPool, firmID, ownerID, logistics.ListDeliveriesOptions{Status: logistics.DeliveryStatusInTransit})
	if err != nil {
		t.Fatalf("ListDeliveries (in_transit filter): %v", err)
	}
	if len(inTransitOnly) != 1 || inTransitOnly[0].ID != delivery.ID {
		t.Fatalf("expected the delivery under the in_transit filter, got %+v", inTransitOnly)
	}
}
