// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package webhook_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/webhook"
)

// These tests exercise the webhooks foundation against a real Postgres
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
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run webhook integration tests")
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
			webhooks, webhook_deliveries, audit_log, permissions CASCADE
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

// waitForDelivery polls webhook_deliveries for webhookID until exactly one
// row appears (or the deadline passes) - Dispatch's own async, fire-and-
// forget design means the test can't just synchronously assert right
// after calling it.
func waitForDelivery(t *testing.T, appPool *pgxpool.Pool, firmID, webhookID uuid.UUID) webhook.Delivery {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		deliveries, err := webhook.ListDeliveries(context.Background(), appPool, firmID, ownerIDFor(t, appPool, firmID), webhookID)
		if err == nil && len(deliveries) > 0 {
			return deliveries[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for a webhook delivery to be recorded")
	return webhook.Delivery{}
}

// ownerIDFor looks the firm's own single seeded owner back up - a small
// convenience so waitForDelivery doesn't need userID threaded through
// every call site.
func ownerIDFor(t *testing.T, appPool *pgxpool.Pool, firmID uuid.UUID) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	err := zdb.WithFirmContext(context.Background(), appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT user_id FROM user_firm_roles WHERE firm_id = $1 LIMIT 1`, firmID).Scan(&userID)
	})
	if err != nil {
		t.Fatalf("look up owner: %v", err)
	}
	return userID
}

// TestDispatch_DeliversToSubscribedWebhookWithValidSignature is both the
// "delivery is attempted when the event fires" and "HMAC signature is
// correct" requirements together: a webhook subscribed to
// EventInvoiceCreated receives exactly one POST when Dispatch fires that
// event, and X-ZonaryOS-Signature verifies against the webhook's own
// known secret recomputed independently in this test (not by calling
// back into the package's own unexported sign()).
func TestDispatch_DeliversToSubscribedWebhookWithValidSignature(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Webhook Dispatch Firm", "owner-dispatch")

	var receivedBody []byte
	var receivedSignature string
	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		receivedSignature = r.Header.Get(webhook.SignatureHeader)
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	hook, err := webhook.CreateWebhook(ctx, appPool, firmID, userID, webhook.WebhookInput{
		Name: "invoice hook", URL: server.URL, Events: []string{string(webhook.EventInvoiceCreated)}, IsActive: true,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}

	webhook.Dispatch(appPool, firmID, webhook.EventInvoiceCreated, map[string]any{"invoiceId": "test-invoice"})

	delivery := waitForDelivery(t, appPool, firmID, hook.ID)
	if !delivery.Success {
		t.Fatalf("expected a successful delivery, got %+v", delivery)
	}
	if delivery.EventType != string(webhook.EventInvoiceCreated) {
		t.Fatalf("expected event type %q, got %q", webhook.EventInvoiceCreated, delivery.EventType)
	}
	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("expected exactly one delivery attempt on success (no retry needed), got %d", callCount)
	}

	mac := hmac.New(sha256.New, []byte(hook.Secret))
	mac.Write(receivedBody)
	expectedSignature := hex.EncodeToString(mac.Sum(nil))
	if receivedSignature != expectedSignature {
		t.Fatalf("signature mismatch: got %q, want %q (computed over body %s)", receivedSignature, expectedSignature, receivedBody)
	}

	var decoded map[string]any
	if err := json.Unmarshal(receivedBody, &decoded); err != nil {
		t.Fatalf("delivered body is not valid JSON: %v", err)
	}
	if decoded["event"] != string(webhook.EventInvoiceCreated) {
		t.Fatalf("expected delivered payload event %q, got %v", webhook.EventInvoiceCreated, decoded["event"])
	}
}

// TestDispatch_NotSubscribedWebhookNeverCalled is Dispatch's own
// event-filter: a webhook that only subscribes to EventApprovalPending
// must never receive a POST for EventInvoiceCreated.
func TestDispatch_NotSubscribedWebhookNeverCalled(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Webhook Filter Firm", "owner-filter")

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if _, err := webhook.CreateWebhook(ctx, appPool, firmID, userID, webhook.WebhookInput{
		Name: "approval-only hook", URL: server.URL, Events: []string{string(webhook.EventApprovalPending)}, IsActive: true,
	}); err != nil {
		t.Fatalf("create webhook: %v", err)
	}

	webhook.Dispatch(appPool, firmID, webhook.EventInvoiceCreated, map[string]any{"invoiceId": "test-invoice"})

	// Give any (incorrect) delivery goroutine time to fire before asserting
	// it never did - there's no "no delivery happened" event to wait for,
	// so this is a bounded settle window, not a poll-until-success loop.
	time.Sleep(500 * time.Millisecond)
	if atomic.LoadInt32(&callCount) != 0 {
		t.Fatalf("expected zero delivery attempts to an unsubscribed webhook, got %d", callCount)
	}
}

// TestDispatch_FailedDeliveryRecordsFailureAfterOneRetry is the "delivery
// record is inserted for both success and failure cases" requirement's
// failure half, plus the single-immediate-retry scope boundary: an
// endpoint that always 500s should be called exactly twice (the initial
// attempt plus one retry, see dispatch.go's own doc comment) and recorded
// as success=false.
func TestDispatch_FailedDeliveryRecordsFailureAfterOneRetry(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Webhook Failure Firm", "owner-failure")

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	hook, err := webhook.CreateWebhook(ctx, appPool, firmID, userID, webhook.WebhookInput{
		Name: "failing hook", URL: server.URL, Events: []string{string(webhook.EventInvoiceCreated)}, IsActive: true,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}

	webhook.Dispatch(appPool, firmID, webhook.EventInvoiceCreated, map[string]any{"invoiceId": "test-invoice"})

	delivery := waitForDelivery(t, appPool, firmID, hook.ID)
	if delivery.Success {
		t.Fatalf("expected a failed delivery to be recorded as success=false, got %+v", delivery)
	}
	if delivery.ResponseStatus == nil || *delivery.ResponseStatus != http.StatusInternalServerError {
		t.Fatalf("expected recorded response status 500, got %v", delivery.ResponseStatus)
	}
	if atomic.LoadInt32(&callCount) != 2 {
		t.Fatalf("expected exactly two delivery attempts (initial + one retry), got %d", callCount)
	}
}
