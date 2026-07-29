// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package identity_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// This test requires a LIVE Keycloak instance (via `make dev-up`, which
// imports deploy/keycloak/zonaryos-realm.json) plus a live Postgres with
// migrations applied - it is the one thing internal/identity's other tests
// cannot prove, since they all substitute a fake OIDC provider or an
// already-verified Identity value. It is skipped unless every required env
// var below is set - there is no live Keycloak in the sandbox this PR was
// developed in, so this test has NOT been run here. See docs/DEVELOPMENT.md
// and the PR description: this PR must not be merged until it has been run
// successfully against a real `make dev-up` stack.
func TestKeycloak_FullLoginToFirmScopedTransaction(t *testing.T) {
	issuerURL := os.Getenv("ZONARYOS_TEST_KEYCLOAK_ISSUER_URL")
	clientID := os.Getenv("ZONARYOS_TEST_KEYCLOAK_CLIENT_ID")
	username := os.Getenv("ZONARYOS_TEST_KEYCLOAK_USERNAME")
	password := os.Getenv("ZONARYOS_TEST_KEYCLOAK_PASSWORD")
	adminDSN := os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN := os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")

	if issuerURL == "" || clientID == "" || username == "" || password == "" || adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_KEYCLOAK_ISSUER_URL, _CLIENT_ID, _USERNAME, _PASSWORD, and the ZONARYOS_TEST_*_DATABASE_URL vars must all be set to run the live Keycloak integration test - see docs/DEVELOPMENT.md")
	}

	ctx := context.Background()

	rawToken, err := fetchTokenViaPasswordGrant(t, issuerURL, clientID, username, password)
	if err != nil {
		t.Fatalf("fetch token from Keycloak: %v", err)
	}

	verifier, err := identity.NewVerifier(ctx, issuerURL, clientID)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	id, err := verifier.Verify(ctx, rawToken)
	if err != nil {
		t.Fatalf("Verify real Keycloak token: %v", err)
	}
	if id.Subject == "" {
		t.Fatal("expected a non-empty subject from a real Keycloak token")
	}
	if id.Email != username {
		t.Errorf("expected email %q, got %q", username, id.Email)
	}

	if err := zdb.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	adminPool, err := zdb.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()

	appPool, err := zdb.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	defer appPool.Close()

	userID, err := identity.ResolveOrCreateUser(ctx, appPool, id)
	if err != nil {
		t.Fatalf("ResolveOrCreateUser: %v", err)
	}

	var firmID, roleID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Keycloak Test Firm') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}

	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO roles (firm_id, key, name) VALUES ($1, 'owner', 'Owner') RETURNING id`, firmID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed role + membership: %v", err)
	}

	detail, err := identity.RoleInFirm(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("RoleInFirm (with a real Keycloak-verified identity): %v", err)
	}
	if detail.RoleKey != "owner" {
		t.Errorf("expected role key 'owner', got %q", detail.RoleKey)
	}
}

func fetchTokenViaPasswordGrant(t *testing.T, issuerURL, clientID, username, password string) (string, error) {
	t.Helper()

	form := url.Values{
		"grant_type": {"password"},
		"client_id":  {clientID},
		"username":   {username},
		"password":   {password},
	}

	resp, err := http.PostForm(strings.TrimRight(issuerURL, "/")+"/protocol/openid-connect/token", form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Error != "" {
		return "", &tokenEndpointError{code: body.Error, description: body.ErrorDesc}
	}
	return body.AccessToken, nil
}

type tokenEndpointError struct {
	code        string
	description string
}

func (e *tokenEndpointError) Error() string {
	return e.code + ": " + e.description
}
