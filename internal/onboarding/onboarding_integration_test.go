// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package onboarding_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	"github.com/moonstreamtech/ZonaryOS/internal/onboarding"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise onboarding progress tracking (including its
// cross-package wiring into internal/inventory.CreateProduct) against a
// real Postgres instance - RLS isolation and permission enforcement can't
// be meaningfully faked, same convention as every other package's
// integration tests in this codebase. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run onboarding integration tests")
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
			user_onboarding_progress, products, stock_levels, audit_log, permissions CASCADE
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

func TestGetProgress_StartsEmpty(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Acme", "sub-empty")

	progress, err := onboarding.GetProgress(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("GetProgress: %v", err)
	}
	if len(progress.CompletedSteps) != 0 {
		t.Fatalf("expected no completed steps, got %v", progress.CompletedSteps)
	}
	if progress.DismissedAt != nil {
		t.Fatalf("expected not dismissed, got %v", *progress.DismissedAt)
	}
}

func TestDismiss_PersistsAndIsIdempotent(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Acme", "sub-dismiss")

	if _, err := onboarding.Dismiss(ctx, appPool, firmID, userID); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	progress, err := onboarding.GetProgress(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("GetProgress: %v", err)
	}
	if progress.DismissedAt == nil {
		t.Fatalf("expected dismissed_at to be set")
	}

	// Dismissing again must not error - the widget can be re-rendered
	// (and its dismiss button re-clicked) any number of times.
	if _, err := onboarding.Dismiss(ctx, appPool, firmID, userID); err != nil {
		t.Fatalf("second Dismiss: %v", err)
	}
}

// TestCreateProduct_AutoCompletesAddFirstProductStep is the batch's own
// required test case: "creating a product auto-completes
// add_first_product" - proof that internal/inventory.CreateProduct's
// onboarding.CompleteStep call (added by this same batch) actually
// reaches user_onboarding_progress, without internal/inventory importing
// this package's internals or this package importing internal/inventory's.
func TestCreateProduct_AutoCompletesAddFirstProductStep(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Acme", "sub-product")

	if _, err := inventory.CreateProduct(ctx, appPool, firmID, userID, inventory.CreateProductInput{
		SKU: "SKU-1", Name: "Widget",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	// CompleteStep runs in its own goroutine (fire-and-forget, same
	// convention as internal/webhook.Dispatch) - poll briefly rather than
	// asserting immediately after CreateProduct returns.
	deadline := time.Now().Add(2 * time.Second)
	for {
		progress, err := onboarding.GetProgress(ctx, appPool, firmID, userID)
		if err != nil {
			t.Fatalf("GetProgress: %v", err)
		}
		for _, s := range progress.CompletedSteps {
			if s == onboarding.StepAddFirstProduct {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected add_first_product to be auto-completed, got %v", progress.CompletedSteps)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
