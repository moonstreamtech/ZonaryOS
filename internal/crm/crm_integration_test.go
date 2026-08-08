// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package crm_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/crm"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise the CRM core against a real Postgres instance -
// RLS isolation and permission enforcement can't be meaningfully faked,
// same convention as every other package's integration tests in this
// codebase. Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL and
// ZONARYOS_TEST_APP_DATABASE_URL are set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run crm integration tests")
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
			customers, audit_log, permissions CASCADE
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

func TestCreateCustomer_OwnerCanCreateAndMemberCanList(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "crm-owner")
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "crm-member")

	customer, err := crm.CreateCustomer(ctx, appPool, firmID, ownerID, crm.CreateCustomerInput{
		Name: "Acme Inc", Email: "billing@acme.example",
	})
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}
	if customer.Name != "Acme Inc" || customer.Currency != "TRY" || customer.SourceWorkflowInstance != nil {
		t.Errorf("unexpected customer: %+v", customer)
	}

	if _, err := crm.CreateCustomer(ctx, appPool, firmID, memberID, crm.CreateCustomerInput{
		Name: "Should Be Denied",
	}); !errors.Is(err, crm.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a non-owner CreateCustomer, got %v", err)
	}

	customers, err := crm.ListCustomers(ctx, appPool, firmID, memberID)
	if err != nil {
		t.Fatalf("ListCustomers (member): %v", err)
	}
	if len(customers) != 1 || customers[0].ID != customer.ID {
		t.Fatalf("expected the member to see the one seeded customer, got %+v", customers)
	}
}

func TestGetCustomerNameTx_ResolvesRealCustomerOnly(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "crm-name-owner")
	customer, err := crm.CreateCustomer(ctx, appPool, firmID, ownerID, crm.CreateCustomerInput{Name: "Wayne Enterprises"})
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}

	var name string
	var ok bool
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		name, ok, err = crm.GetCustomerNameTx(ctx, tx, firmID, customer.ID)
		return err
	})
	if err != nil {
		t.Fatalf("GetCustomerNameTx: %v", err)
	}
	if !ok || name != "Wayne Enterprises" {
		t.Errorf("expected to resolve %q, got ok=%v name=%q", "Wayne Enterprises", ok, name)
	}

	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		_, ok, err = crm.GetCustomerNameTx(ctx, tx, firmID, uuid.New())
		return err
	})
	if err != nil {
		t.Fatalf("GetCustomerNameTx (nonexistent): %v", err)
	}
	if ok {
		t.Errorf("expected ok=false for a nonexistent customer id")
	}
}
