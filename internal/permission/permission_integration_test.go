package permission_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise permission checks against a real Postgres
// instance, same convention as this repo's other integration tests -
// skipped unless both ZONARYOS_TEST_ADMIN_DATABASE_URL and
// ZONARYOS_TEST_APP_DATABASE_URL are set. See docs/DEVELOPMENT.md.

func setupTest(t *testing.T) (adminPool, appPool *pgxpool.Pool) {
	t.Helper()
	adminDSN := os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN := os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run permission integration tests")
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
		TRUNCATE firms, users, roles, role_permissions, user_firm_roles CASCADE
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

// TestIsMember_TrueForARealMember and the tests below cover
// permission.IsMember directly: the membership check every read path
// that isn't already gated by Has (a specific permission) must perform
// before trusting a caller-supplied firmID - see internal/workflow's
// CurrentState/ListInstances/LookupDefinitionByKey doc comments for why
// this exists (a real cross-firm data leak this closes).
func TestIsMember_TrueForARealMember(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID, userID, roleID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ('sub-member', 'sub-member@example.com', 'Member') RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO roles (firm_id, key, name) VALUES ($1, 'tester', 'Tester') RETURNING id
		`, firmID).Scan(&roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO user_firm_roles (firm_id, user_id, role_id) VALUES ($1, $2, $3)
		`, firmID, userID, roleID)
		return err
	})
	if err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			t.Error("expected a real member to be reported as a member")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("IsMember: %v", err)
	}
}

// TestIsMember_FalseForANonMember is the exact scenario the vulnerability
// looked like: an authenticated user (a real user row, just not a member
// of this particular firm) queried under a firm context set from that
// firm's genuine ID. IsMember must say false - RLS being satisfied by
// the ID being real is not the same as the caller belonging to it.
func TestIsMember_FalseForANonMember(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var firmID, userID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ('sub-outsider', 'sub-outsider@example.com', 'Outsider') RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// Deliberately no user_firm_roles row for (userID, firmID).

	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if isMember {
			t.Error("expected a non-member to be reported as not a member, even under the firm's own valid ID")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("IsMember: %v", err)
	}
}

func TestIsMember_FalseForAnUnknownFirm(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	var userID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ('sub-no-firm', 'sub-no-firm@example.com', 'NoFirm') RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	firmID := uuid.New()
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if isMember {
			t.Error("expected no membership for a firm ID that doesn't exist at all")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("IsMember: %v", err)
	}
}
