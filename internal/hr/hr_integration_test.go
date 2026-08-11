// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package hr_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/hr"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise the HR core against a real Postgres instance - RLS
// isolation and permission enforcement can't be meaningfully faked, same
// convention as every other package's integration tests in this
// codebase. Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL and
// ZONARYOS_TEST_APP_DATABASE_URL are set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run hr integration tests")
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
			people, contracts, audit_log, permissions CASCADE
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
// holding it - mirrors internal/accounting's own test fixture shape.
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

func TestCreatePerson_OwnerCanCreateAndMemberCanList(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "hr-owner")
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "hr-member")

	person, err := hr.CreatePerson(ctx, appPool, firmID, ownerID, hr.CreatePersonInput{
		FullName: "Ada Lovelace", Type: hr.PersonTypeEmployee, Email: "ada@example.com", StartDate: "2024-01-15",
	})
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}
	if person.FullName != "Ada Lovelace" || person.Type != hr.PersonTypeEmployee || person.Status != hr.PersonStatusActive {
		t.Errorf("unexpected person: %+v", person)
	}
	if person.StartDate == nil || person.StartDate.Format("2006-01-02") != "2024-01-15" {
		t.Errorf("expected start date 2024-01-15, got %v", person.StartDate)
	}

	// A non-owner member must be denied write access.
	if _, err := hr.CreatePerson(ctx, appPool, firmID, memberID, hr.CreatePersonInput{
		FullName: "Should Be Denied", Type: hr.PersonTypeEmployee,
	}); !errors.Is(err, hr.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a non-owner CreatePerson, got %v", err)
	}

	// But a member CAN read the roster.
	people, err := hr.ListPeople(ctx, appPool, firmID, memberID, hr.ListPeopleOptions{})
	if err != nil {
		t.Fatalf("ListPeople (member): %v", err)
	}
	if len(people) != 1 || people[0].ID != person.ID {
		t.Fatalf("expected the member to see the one seeded person, got %+v", people)
	}
}

func TestUpdatePerson_PartialUpdateAndStatusFilter(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "hr-owner-2")

	person, err := hr.CreatePerson(ctx, appPool, firmID, ownerID, hr.CreatePersonInput{
		FullName: "Grace Hopper", Type: hr.PersonTypeContractor,
	})
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}

	inactive := hr.PersonStatusInactive
	updated, err := hr.UpdatePerson(ctx, appPool, firmID, ownerID, person.ID, hr.PersonUpdate{Status: &inactive})
	if err != nil {
		t.Fatalf("UpdatePerson: %v", err)
	}
	if updated.Status != hr.PersonStatusInactive {
		t.Errorf("expected status inactive, got %q", updated.Status)
	}
	if updated.FullName != "Grace Hopper" {
		t.Errorf("expected full name to remain unchanged by a status-only update, got %q", updated.FullName)
	}

	activeOnly, err := hr.ListPeople(ctx, appPool, firmID, ownerID, hr.ListPeopleOptions{Status: hr.PersonStatusActive})
	if err != nil {
		t.Fatalf("ListPeople (active only): %v", err)
	}
	if len(activeOnly) != 0 {
		t.Fatalf("expected zero active people after deactivation, got %+v", activeOnly)
	}

	inactiveOnly, err := hr.ListPeople(ctx, appPool, firmID, ownerID, hr.ListPeopleOptions{Status: hr.PersonStatusInactive})
	if err != nil {
		t.Fatalf("ListPeople (inactive only): %v", err)
	}
	if len(inactiveOnly) != 1 || inactiveOnly[0].ID != person.ID {
		t.Fatalf("expected the one inactive person, got %+v", inactiveOnly)
	}
}

func TestCreateContract_AttachesToPersonAndRejectsUnknownPerson(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "hr-owner-3")

	person, err := hr.CreatePerson(ctx, appPool, firmID, ownerID, hr.CreatePersonInput{
		FullName: "Alan Turing", Type: hr.PersonTypeEmployee,
	})
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}

	contract, err := hr.CreateContract(ctx, appPool, firmID, ownerID, person.ID, hr.CreateContractInput{
		Type: "salary", Amount: "15000.00", StartDate: "2024-02-01",
	})
	if err != nil {
		t.Fatalf("CreateContract: %v", err)
	}
	if contract.Type != "salary" || contract.Amount == nil || *contract.Amount != "15000.0000" {
		t.Errorf("unexpected contract: %+v", contract)
	}
	if contract.Currency != "TRY" {
		t.Errorf("expected default currency TRY, got %q", contract.Currency)
	}

	contracts, err := hr.ListContracts(ctx, appPool, firmID, ownerID, person.ID)
	if err != nil {
		t.Fatalf("ListContracts: %v", err)
	}
	if len(contracts) != 1 || contracts[0].ID != contract.ID {
		t.Fatalf("expected exactly one contract, got %+v", contracts)
	}

	if _, err := hr.CreateContract(ctx, appPool, firmID, ownerID, uuid.New(), hr.CreateContractInput{
		Type: "salary", StartDate: "2024-02-01",
	}); !errors.Is(err, hr.ErrPersonNotFound) {
		t.Fatalf("expected ErrPersonNotFound for a nonexistent person, got %v", err)
	}
}

func TestPeople_CrossFirmIsolation(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmA, ownerA := seedOwner(ctx, t, adminPool, appPool, "Firm A", "hr-cross-a")
	firmB, ownerB := seedOwner(ctx, t, adminPool, appPool, "Firm B", "hr-cross-b")

	if _, err := hr.CreatePerson(ctx, appPool, firmA, ownerA, hr.CreatePersonInput{
		FullName: "Firm A Person", Type: hr.PersonTypeEmployee,
	}); err != nil {
		t.Fatalf("CreatePerson (firm A): %v", err)
	}

	peopleB, err := hr.ListPeople(ctx, appPool, firmB, ownerB, hr.ListPeopleOptions{})
	if err != nil {
		t.Fatalf("ListPeople (firm B): %v", err)
	}
	if len(peopleB) != 0 {
		t.Fatalf("expected firm B to see no people from firm A, got %+v", peopleB)
	}

	if _, err := hr.ListPeople(ctx, appPool, firmA, ownerB, hr.ListPeopleOptions{}); !errors.Is(err, hr.ErrFirmNotFound) {
		t.Fatalf("expected ErrFirmNotFound when firm B's owner reads firm A's people, got %v", err)
	}
}

// TestListPeople_SearchFindsByPhoneFragment is Part 1's own required
// test case: full-text search (migrations/0024's search_tsv) finds a
// person by a phone number fragment.
func TestListPeople_SearchFindsByPhoneFragment(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm Search", "hr-search-owner")

	if _, err := hr.CreatePerson(ctx, appPool, firmID, ownerID, hr.CreatePersonInput{
		FullName: "Grace Hopper", Type: hr.PersonTypeEmployee, Phone: "+90 555 123 4567",
	}); err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}
	if _, err := hr.CreatePerson(ctx, appPool, firmID, ownerID, hr.CreatePersonInput{
		FullName: "Alan Turing", Type: hr.PersonTypeEmployee, Phone: "+90 555 987 6543",
	}); err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}

	people, err := hr.ListPeople(ctx, appPool, firmID, ownerID, hr.ListPeopleOptions{Search: "4567"})
	if err != nil {
		t.Fatalf("ListPeople (search): %v", err)
	}
	if len(people) != 1 || people[0].FullName != "Grace Hopper" {
		t.Fatalf("expected search '4567' to match only Grace Hopper, got %+v", people)
	}
}
