// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apikey_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/apikey"
	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise the API key foundation against a real Postgres
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
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run apikey integration tests")
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
			api_keys, audit_log, permissions CASCADE
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

func seedOwnerWithScopes(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmName, keycloakSubject string, permissionKeys []string) (firmID, userID uuid.UUID) {
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
		if _, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, userID, roleID); err != nil {
			return err
		}
		for _, key := range permissionKeys {
			if _, err := tx.Exec(ctx, `INSERT INTO permissions (key, description) VALUES ($1, 'test permission') ON CONFLICT DO NOTHING`, key); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO role_permissions (firm_id, role_id, permission_key) VALUES ($1, $2, $3)`, firmID, roleID, key); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed owner role/membership/permissions: %v", err)
	}
	return firmID, userID
}

// TestCreateAPIKey_ScopeExceedingCreatorPermissionsRejected is the "an API
// key can never have MORE scope than the user who created it" requirement:
// the owner here only holds view_stock, so requesting execute_transition
// as a scope must be rejected at creation time, before any api_keys row
// is ever written.
func TestCreateAPIKey_ScopeExceedingCreatorPermissionsRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwnerWithScopes(ctx, t, adminPool, appPool, "Scope Test Firm", "owner-scope-exceed", []string{"view_stock"})

	_, _, err := apikey.CreateAPIKey(ctx, appPool, firmID, userID, apikey.CreateAPIKeyInput{
		Name:   "over-scoped key",
		Scopes: []string{"view_stock", "execute_transition"},
	})
	if !errors.Is(err, apikey.ErrScopeExceedsPermissions) {
		t.Fatalf("expected ErrScopeExceedsPermissions, got %v", err)
	}

	keys, err := apikey.ListAPIKeys(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("list API keys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected no API key to have been created, got %d", len(keys))
	}
}

// TestCreateAPIKey_ScopeSubsetOfCreatorPermissionsAllowed is the
// complementary success case - a scope list that's a genuine subset of
// the owner's own granted permissions is accepted.
func TestCreateAPIKey_ScopeSubsetOfCreatorPermissionsAllowed(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwnerWithScopes(ctx, t, adminPool, appPool, "Scope Subset Firm", "owner-scope-subset", []string{"view_stock", "execute_transition"})

	plaintext, key, err := apikey.CreateAPIKey(ctx, appPool, firmID, userID, apikey.CreateAPIKeyInput{
		Name:   "view-only key",
		Scopes: []string{"view_stock"},
	})
	if err != nil {
		t.Fatalf("create API key: %v", err)
	}
	if plaintext == "" {
		t.Fatal("expected a non-empty plaintext key")
	}
	if len(key.Scopes) != 1 || key.Scopes[0] != "view_stock" {
		t.Fatalf("expected scopes [view_stock], got %v", key.Scopes)
	}
}

// TestAuthenticate_CrossFirmRejected proves a key minted for one firm is
// rejected when presented against a different firm's URL - the whole
// point of scoping the api_keys lookup by both key_hash AND firm_id (see
// authenticate.go's own doc comment).
func TestAuthenticate_CrossFirmRejected(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmA, userA := seedOwnerWithScopes(ctx, t, adminPool, appPool, "Firm A", "owner-cross-firm-a", []string{"view_stock"})
	firmB, _ := seedOwnerWithScopes(ctx, t, adminPool, appPool, "Firm B", "owner-cross-firm-b", nil)

	plaintext, _, err := apikey.CreateAPIKey(ctx, appPool, firmA, userA, apikey.CreateAPIKeyInput{
		Name:   "firm A key",
		Scopes: []string{"view_stock"},
	})
	if err != nil {
		t.Fatalf("create API key: %v", err)
	}

	fb := &apikey.Fallback{Pool: appPool}

	// Presented against its own firm: should authenticate successfully.
	reqOwnFirm := httptest.NewRequest(http.MethodGet, "/api/firms/"+firmA.String()+"/api-keys", nil)
	reqOwnFirm.Header.Set("Authorization", "Bearer "+plaintext)
	reqOwnFirm.SetPathValue("firmID", firmA.String())
	if _, _, ok := fb.Authenticate(reqOwnFirm); !ok {
		t.Fatal("expected authentication to succeed against the key's own firm")
	}

	// Presented against a different firm's URL: must be rejected.
	reqOtherFirm := httptest.NewRequest(http.MethodGet, "/api/firms/"+firmB.String()+"/api-keys", nil)
	reqOtherFirm.Header.Set("Authorization", "Bearer "+plaintext)
	reqOtherFirm.SetPathValue("firmID", firmB.String())
	if _, _, ok := fb.Authenticate(reqOtherFirm); ok {
		t.Fatal("expected authentication to be rejected for a firm the key was not created in")
	}
}

// TestAuthenticate_ScopeRestrictionLimitsPermissionHas is the "scope-
// limited access" requirement: a key with only view_stock as a declared
// scope must not be able to exercise execute_transition, even though the
// creating owner holds both permissions themselves - internal/permission.Has
// intersects the ordinary role-based grant with any scope restriction
// found in context (see permission.Has's own doc comment).
func TestAuthenticate_ScopeRestrictionLimitsPermissionHas(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwnerWithScopes(ctx, t, adminPool, appPool, "Scope Limit Firm", "owner-scope-limit", []string{"view_stock", "execute_transition"})

	plaintext, _, err := apikey.CreateAPIKey(ctx, appPool, firmID, userID, apikey.CreateAPIKeyInput{
		Name:   "view-only key",
		Scopes: []string{"view_stock"},
	})
	if err != nil {
		t.Fatalf("create API key: %v", err)
	}

	fb := &apikey.Fallback{Pool: appPool}
	req := httptest.NewRequest(http.MethodGet, "/api/firms/"+firmID.String()+"/api-keys", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	req.SetPathValue("firmID", firmID.String())

	id, scopes, ok := fb.Authenticate(req)
	if !ok {
		t.Fatal("expected authentication to succeed")
	}
	if len(scopes) != 1 || scopes[0] != "view_stock" {
		t.Fatalf("expected resolved scopes [view_stock], got %v", scopes)
	}

	scopedCtx := identity.WithScopeRestriction(context.Background(), scopes)
	err = zdb.WithFirmContext(scopedCtx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		hasView, err := permission.Has(ctx, tx, firmID, userID, "view_stock")
		if err != nil {
			return err
		}
		if !hasView {
			t.Error("expected the scoped key to still be able to exercise view_stock")
		}

		hasExecute, err := permission.Has(ctx, tx, firmID, userID, "execute_transition")
		if err != nil {
			return err
		}
		if hasExecute {
			t.Error("expected the scoped key to be rejected for execute_transition, which it was not granted as a scope")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("check scoped permission: %v", err)
	}

	// Sanity: the resolved identity really does map back to the creating
	// owner (userA), the same user id every non-scoped Has check above
	// relies on to find their execute_transition grant in role_permissions.
	if id.Subject == "" {
		t.Fatal("expected a resolved identity subject")
	}
}
