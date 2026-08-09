// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package notification_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/notification"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise the notification inbox against a real Postgres
// instance - RLS isolation and the recipient-scoping filter can't be
// meaningfully faked, same convention as every other package's
// integration tests in this codebase. Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func testDSNs(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run notification integration tests")
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
			notifications, audit_log, permissions CASCADE
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

func seedFirmAndMember(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmName, keycloakSubject string) (firmID, userID uuid.UUID) {
	t.Helper()
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ($1) RETURNING id`, firmName).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test User").Scan(&userID); err != nil {
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
	return firmID, userID
}

func createNotification(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID, recipientID uuid.UUID) notification.Notification {
	t.Helper()
	var n notification.Notification
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		n, err = notification.CreateTx(ctx, tx, firmID, recipientID, "approval_pending", "Approval needed", "A rule needs your approval.", map[string]any{"instanceId": uuid.NewString()})
		return err
	})
	if err != nil {
		t.Fatalf("CreateTx: %v", err)
	}
	return n
}

func TestListForUser_OnlyVisibleToOwnFirmAndRecipient(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmA, userA := seedFirmAndMember(ctx, t, adminPool, appPool, "Firm A", "notif-user-a")
	_, userAOther := firmA, seedMemberOnly(ctx, t, adminPool, appPool, firmA, "notif-user-a2")
	_, userB := seedFirmAndMember(ctx, t, adminPool, appPool, "Firm B", "notif-user-b")

	createNotification(ctx, t, appPool, firmA, userA)
	createNotification(ctx, t, appPool, firmA, userAOther)

	// userA sees only their own notification within firm A, not the one
	// addressed to userAOther in the same firm.
	resultA, err := notification.ListForUser(ctx, appPool, firmA, userA, notification.ListOptions{})
	if err != nil {
		t.Fatalf("ListForUser (userA): %v", err)
	}
	if len(resultA.Notifications) != 1 {
		t.Fatalf("expected userA to see exactly 1 notification, got %d", len(resultA.Notifications))
	}
	if resultA.Notifications[0].RecipientUserID != userA {
		t.Fatalf("expected userA's own notification, got recipient %s", resultA.Notifications[0].RecipientUserID)
	}

	// firm B has no notifications of its own, and userB can't see firm
	// A's notifications by supplying a real firm A id either - not a
	// member of it.
	_, err = notification.ListForUser(ctx, appPool, firmA, userB, notification.ListOptions{})
	if !errors.Is(err, notification.ErrFirmNotFound) {
		t.Fatalf("expected ErrFirmNotFound for a non-member of firm A, got %v", err)
	}
}

func seedMemberOnly(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmID uuid.UUID, keycloakSubject string) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := adminPool.QueryRow(ctx, `
		INSERT INTO users (keycloak_subject, email, display_name) VALUES ($1, $2, $3) RETURNING id
	`, keycloakSubject, keycloakSubject+"@example.com", "Test Member").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var roleID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO roles (firm_id, key, name) VALUES ($1, 'member2', 'Member2') RETURNING id`, firmID).Scan(&roleID); err != nil {
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

func TestUnreadCount_DecrementsOnRead(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, userID := seedFirmAndMember(ctx, t, adminPool, appPool, "Firm A", "notif-unread-user")

	n1 := createNotification(ctx, t, appPool, firmID, userID)
	createNotification(ctx, t, appPool, firmID, userID)

	count, err := notification.UnreadCount(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("UnreadCount: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 unread notifications, got %d", count)
	}

	if err := notification.MarkRead(ctx, appPool, firmID, userID, n1.ID); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	count, err = notification.UnreadCount(ctx, appPool, firmID, userID)
	if err != nil {
		t.Fatalf("UnreadCount after read: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 unread notification after marking one read, got %d", count)
	}

	// Marking someone else's notification read is rejected as not found.
	otherUser := seedMemberOnly(ctx, t, adminPool, appPool, firmID, "notif-unread-other")
	n2 := createNotification(ctx, t, appPool, firmID, otherUser)
	if err := notification.MarkRead(ctx, appPool, firmID, userID, n2.ID); !errors.Is(err, notification.ErrNotificationNotFound) {
		t.Fatalf("expected ErrNotificationNotFound marking another recipient's notification read, got %v", err)
	}
}
