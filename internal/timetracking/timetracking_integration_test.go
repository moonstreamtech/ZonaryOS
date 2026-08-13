// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package timetracking_test

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
	"github.com/moonstreamtech/ZonaryOS/internal/timetracking"
)

// These tests exercise time tracking against a real Postgres instance -
// RLS isolation and permission enforcement can't be meaningfully faked,
// same convention as every other package's integration tests in this
// codebase. Skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL and
// ZONARYOS_TEST_APP_DATABASE_URL are set. See docs/DEVELOPMENT.md.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run time tracking integration tests")
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
			people, contracts, time_entries, audit_log, permissions CASCADE
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

// seedMember seeds a member user under a role keyed by keycloakSubject
// itself (not a fixed "member" key) - this file's tests need more than
// one distinct member in the same firm, and roles.(firm_id, key) is
// unique, so a shared fixed key would collide on the second call.
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
		if err := tx.QueryRow(ctx, `INSERT INTO roles (firm_id, key, name) VALUES ($1, $2, 'Member') RETURNING id`, firmID, keycloakSubject).Scan(&roleID); err != nil {
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

func seedPerson(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID, ownerID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	p, err := hr.CreatePerson(ctx, appPool, firmID, ownerID, hr.CreatePersonInput{FullName: name, Type: hr.PersonTypeEmployee})
	if err != nil {
		t.Fatalf("CreatePerson: %v", err)
	}
	return p.ID
}

// TestSummary_AggregatesHoursPerPerson is Part 2's own required test
// case: seed a few entries across people/dates, assert the summary
// total.
func TestSummary_AggregatesHoursPerPerson(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm Time", "time-owner")
	person1 := seedPerson(ctx, t, appPool, firmID, ownerID, "Ada Lovelace")
	person2 := seedPerson(ctx, t, appPool, firmID, ownerID, "Alan Turing")

	for _, e := range []struct {
		person uuid.UUID
		date   string
		hours  string
	}{
		{person1, "2026-08-01", "4.00"},
		{person1, "2026-08-02", "3.50"},
		{person2, "2026-08-01", "8.00"},
		{person2, "2026-08-05", "2.00"}, // outside the from/to window below
	} {
		if _, err := timetracking.CreateEntry(ctx, appPool, firmID, ownerID, timetracking.EntryInput{
			PersonID: e.person, Date: e.date, Hours: e.hours,
		}); err != nil {
			t.Fatalf("CreateEntry: %v", err)
		}
	}

	totals, err := timetracking.Summary(ctx, appPool, firmID, ownerID, timetracking.SummaryOptions{From: "2026-08-01", To: "2026-08-02"})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	byPerson := map[uuid.UUID]string{}
	for _, tot := range totals {
		byPerson[tot.PersonID] = tot.TotalHours
	}
	if byPerson[person1] != "7.50" {
		t.Errorf("expected person1 total 7.50, got %q", byPerson[person1])
	}
	if byPerson[person2] != "8.00" {
		t.Errorf("expected person2 total 8.00 (2026-08-05 entry excluded by the window), got %q", byPerson[person2])
	}
}

// TestUpdateEntry_OnlyCreatorOrOwnerMayEdit is Part 2's own required test
// case: a person can only edit their own entries.
func TestUpdateEntry_OnlyCreatorOrOwnerMayEdit(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm Time B", "time-owner-2")
	memberAID := seedMember(ctx, t, adminPool, appPool, firmID, "time-member-a")
	memberBID := seedMember(ctx, t, adminPool, appPool, firmID, "time-member-b")
	person := seedPerson(ctx, t, appPool, firmID, ownerID, "Grace Hopper")

	entry, err := timetracking.CreateEntry(ctx, appPool, firmID, memberAID, timetracking.EntryInput{
		PersonID: person, Date: "2026-08-10", Hours: "5.00",
	})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}

	// memberB (not the creator) must be denied.
	if _, err := timetracking.UpdateEntry(ctx, appPool, firmID, memberBID, entry.ID, timetracking.EntryInput{Hours: "6.00"}); !errors.Is(err, timetracking.ErrNotEntryOwner) {
		t.Fatalf("expected ErrNotEntryOwner for a non-creator update, got %v", err)
	}
	if err := timetracking.DeleteEntry(ctx, appPool, firmID, memberBID, entry.ID); !errors.Is(err, timetracking.ErrNotEntryOwner) {
		t.Fatalf("expected ErrNotEntryOwner for a non-creator delete, got %v", err)
	}

	// The creator CAN edit their own entry.
	updated, err := timetracking.UpdateEntry(ctx, appPool, firmID, memberAID, entry.ID, timetracking.EntryInput{Hours: "6.00"})
	if err != nil {
		t.Fatalf("UpdateEntry (own entry): %v", err)
	}
	if updated.Hours != "6.00" {
		t.Errorf("expected hours 6.00, got %q", updated.Hours)
	}

	// An owner can also edit someone else's entry.
	if _, err := timetracking.UpdateEntry(ctx, appPool, firmID, ownerID, entry.ID, timetracking.EntryInput{Hours: "7.00"}); err != nil {
		t.Fatalf("UpdateEntry (owner override): %v", err)
	}
}
