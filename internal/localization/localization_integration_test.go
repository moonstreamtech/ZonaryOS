// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package localization_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/localization"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise the address/tax rate CRUD against a real Postgres
// instance, same convention as every other package's integration tests
// in this codebase. Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL
// and ZONARYOS_TEST_APP_DATABASE_URL are set. See docs/DEVELOPMENT.md.

func setupTest(t *testing.T) (adminPool, appPool *pgxpool.Pool) {
	t.Helper()
	adminDSN := os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN := os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run localization integration tests")
	}
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
			addresses, tax_rates, audit_log, permissions CASCADE
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
		if err := tx.QueryRow(ctx, `INSERT INTO roles (firm_id, key, name, is_owner) VALUES ($1, 'owner', 'Owner', true) RETURNING id`, firmID).Scan(&roleID); err != nil {
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

func TestAddress_CRUD(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "loc-owner")
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "loc-member")

	addr, err := localization.CreateAddress(ctx, appPool, firmID, ownerID, localization.AddressInput{
		Label: "billing", Line1: "123 Main St", City: "Istanbul", CountryCode: "tr", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("CreateAddress: %v", err)
	}
	if addr.CountryCode != "TR" {
		t.Errorf("expected countryCode to be normalized to uppercase, got %q", addr.CountryCode)
	}

	if _, err := localization.CreateAddress(ctx, appPool, firmID, memberID, localization.AddressInput{
		Line1: "Should be denied", City: "X", CountryCode: "TR",
	}); !errors.Is(err, localization.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a non-owner CreateAddress, got %v", err)
	}

	addresses, err := localization.ListAddresses(ctx, appPool, firmID, memberID)
	if err != nil {
		t.Fatalf("ListAddresses (member): %v", err)
	}
	if len(addresses) != 1 || addresses[0].ID != addr.ID {
		t.Fatalf("expected the member to see the one seeded address, got %+v", addresses)
	}

	updated, err := localization.UpdateAddress(ctx, appPool, firmID, ownerID, addr.ID, localization.AddressInput{
		Line1: "456 Side St", City: "Ankara", CountryCode: "TR",
	})
	if err != nil {
		t.Fatalf("UpdateAddress: %v", err)
	}
	if updated.City != "Ankara" {
		t.Errorf("expected updated city Ankara, got %q", updated.City)
	}

	if err := localization.DeleteAddress(ctx, appPool, firmID, ownerID, addr.ID); err != nil {
		t.Fatalf("DeleteAddress: %v", err)
	}
	addressesAfter, err := localization.ListAddresses(ctx, appPool, firmID, ownerID)
	if err != nil {
		t.Fatalf("ListAddresses after delete: %v", err)
	}
	if len(addressesAfter) != 0 {
		t.Fatalf("expected no addresses after delete, got %+v", addressesAfter)
	}
}

func TestTaxRate_CRUD(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm B", "loc-owner-2")
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "loc-member-2")

	rate, err := localization.CreateTaxRate(ctx, appPool, firmID, ownerID, localization.TaxRateInput{
		Name: "Standard VAT", Rate: "0.2000", CountryCode: "TR", AppliesTo: "goods", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("CreateTaxRate: %v", err)
	}
	if rate.Rate != "0.2000" {
		t.Errorf("expected rate 0.2000, got %q", rate.Rate)
	}

	if _, err := localization.CreateTaxRate(ctx, appPool, firmID, memberID, localization.TaxRateInput{
		Name: "Should be denied", Rate: "0.1",
	}); !errors.Is(err, localization.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a non-owner CreateTaxRate, got %v", err)
	}

	rates, err := localization.ListTaxRates(ctx, appPool, firmID, memberID)
	if err != nil {
		t.Fatalf("ListTaxRates (member): %v", err)
	}
	if len(rates) != 1 || rates[0].ID != rate.ID {
		t.Fatalf("expected the member to see the one seeded tax rate, got %+v", rates)
	}

	updated, err := localization.UpdateTaxRate(ctx, appPool, firmID, ownerID, rate.ID, localization.TaxRateInput{
		Name: "Reduced VAT", Rate: "0.1000",
	})
	if err != nil {
		t.Fatalf("UpdateTaxRate: %v", err)
	}
	if updated.Rate != "0.1000" {
		t.Errorf("expected updated rate 0.1000, got %q", updated.Rate)
	}

	if err := localization.DeleteTaxRate(ctx, appPool, firmID, ownerID, rate.ID); err != nil {
		t.Fatalf("DeleteTaxRate: %v", err)
	}
	ratesAfter, err := localization.ListTaxRates(ctx, appPool, firmID, ownerID)
	if err != nil {
		t.Fatalf("ListTaxRates after delete: %v", err)
	}
	if len(ratesAfter) != 0 {
		t.Fatalf("expected no tax rates after delete, got %+v", ratesAfter)
	}
}
