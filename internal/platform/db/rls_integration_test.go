package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise Row-Level Security against a real Postgres instance
// (Never-Violate Rule 3: RLS is mandatory, application-level tenant_id
// filtering alone is not an acceptable substitute). They are skipped unless
// both ZONARYOS_TEST_ADMIN_DATABASE_URL (a superuser/owner DSN, used to run
// migrations and seed data outside of RLS) and ZONARYOS_TEST_APP_DATABASE_URL
// (the unprivileged zonaryos_app role DSN, the one RLS actually applies to)
// are set. See docs/DEVELOPMENT.md.

func testDSNs(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run RLS integration tests")
	}
	return adminDSN, appDSN
}

// seedTwoFirms creates two firms (via the admin pool, since `firms` is a
// global, non-RLS table) and one role per firm (via the app pool, going
// through WithFirmContext, exactly as application code would). It returns
// the firm and role IDs.
func seedTwoFirms(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool) (firmA, roleA, firmB, roleB uuid.UUID) {
	t.Helper()

	err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm A') RETURNING id`).Scan(&firmA)
	if err != nil {
		t.Fatalf("seed firm A: %v", err)
	}
	err = adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('Firm B') RETURNING id`).Scan(&firmB)
	if err != nil {
		t.Fatalf("seed firm B: %v", err)
	}

	err = db.WithFirmContext(ctx, appPool, firmA, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO roles (firm_id, key, name) VALUES ($1, 'owner', 'Owner') RETURNING id`, firmA).Scan(&roleA)
	})
	if err != nil {
		t.Fatalf("seed role A: %v", err)
	}

	err = db.WithFirmContext(ctx, appPool, firmB, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO roles (firm_id, key, name) VALUES ($1, 'owner', 'Owner') RETURNING id`, firmB).Scan(&roleB)
	})
	if err != nil {
		t.Fatalf("seed role B: %v", err)
	}

	return firmA, roleA, firmB, roleB
}

func TestRLS_FirmScopedReadsOnlySeeOwnFirm(t *testing.T) {
	adminDSN, appDSN := testDSNs(t)
	ctx := context.Background()

	if err := db.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	adminPool, err := db.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()

	if _, err := adminPool.Exec(ctx, `TRUNCATE firms, users, roles, role_permissions, user_firm_roles CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err := db.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	defer appPool.Close()

	firmA, roleA, _, roleB := seedTwoFirms(ctx, t, adminPool, appPool)

	var seenIDs []uuid.UUID
	err = db.WithFirmContext(ctx, appPool, firmA, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM roles`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			seenIDs = append(seenIDs, id)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("query as firm A: %v", err)
	}

	if len(seenIDs) != 1 || seenIDs[0] != roleA {
		t.Fatalf("expected firm A session to see only role %s, got %v (firm B role was %s)", roleA, seenIDs, roleB)
	}
}

func TestRLS_WritesAreRejectedOutsideOwnFirm(t *testing.T) {
	adminDSN, appDSN := testDSNs(t)
	ctx := context.Background()

	if err := db.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	adminPool, err := db.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()

	if _, err := adminPool.Exec(ctx, `TRUNCATE firms, users, roles, role_permissions, user_firm_roles CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err := db.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	defer appPool.Close()

	firmA, _, firmB, _ := seedTwoFirms(ctx, t, adminPool, appPool)

	// A session scoped to firm A must not be able to insert a row that
	// belongs to firm B, even though the query itself is well-formed -
	// this is the WITH CHECK half of the policy, not just USING.
	err = db.WithFirmContext(ctx, appPool, firmA, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO roles (firm_id, key, name) VALUES ($1, 'intruder', 'Intruder')`, firmB)
		return err
	})
	if err == nil {
		t.Fatal("expected cross-tenant insert to be rejected by RLS, but it succeeded")
	}
}

func TestRLS_NoFirmContextSeesNothing(t *testing.T) {
	adminDSN, appDSN := testDSNs(t)
	ctx := context.Background()

	if err := db.Migrate(adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	adminPool, err := db.Open(ctx, adminDSN)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()

	if _, err := adminPool.Exec(ctx, `TRUNCATE firms, users, roles, role_permissions, user_firm_roles CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	appPool, err := db.Open(ctx, appDSN)
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	defer appPool.Close()

	seedTwoFirms(ctx, t, adminPool, appPool)

	// A query issued without ever calling WithFirmContext (i.e. as if a
	// developer forgot to scope it) must see zero rows - proving isolation
	// does not depend on application code remembering to filter.
	rows, err := appPool.Query(ctx, `SELECT id FROM roles`)
	if err != nil {
		t.Fatalf("query without firm context: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows visible without firm context, got %d", count)
	}
}
