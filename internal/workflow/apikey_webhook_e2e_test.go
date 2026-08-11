// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/moonstreamtech/ZonaryOS/internal/apikey"
	"github.com/moonstreamtech/ZonaryOS/internal/identity"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/webhook"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// TestE2E_APIKeyCreatesInstanceAndWebhookFires is this batch's own
// required end-to-end scenario: an API key (not a Keycloak session)
// creates a workflow instance, and a webhook subscribed to
// workflow.instance.created fires and records a delivery - exercising
// the full chain this batch adds: identity.Middleware's Fallback ->
// internal/apikey.authenticate -> identity.WithScopeRestriction ->
// internal/permission.Has -> workflow.CreateInstance ->
// webhook.Dispatch -> a real signed HTTP delivery.
func TestE2E_APIKeyCreatesInstanceAndWebhookFires(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	// setupTest's own TRUNCATE list (workflow_integration_test.go) doesn't
	// include this batch's own tables - harmless leftovers from another
	// test file wouldn't affect this test's own randomly-generated IDs,
	// but starting clean keeps this test's own assertions (e.g. "exactly
	// one delivery") meaningful regardless of run order.
	if _, err := adminPool.Exec(ctx, `TRUNCATE api_keys, webhooks, webhook_deliveries CASCADE`); err != nil {
		t.Fatalf("truncate api key/webhook tables: %v", err)
	}

	var firmID uuid.UUID
	if err := adminPool.QueryRow(ctx, `INSERT INTO firms (name) VALUES ('E2E Firm') RETURNING id`).Scan(&firmID); err != nil {
		t.Fatalf("seed firm: %v", err)
	}
	definitionID, err := workflow.SeedStockToSaleWorkflow(ctx, appPool, firmID)
	if err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	ownerID, ownerRoleID := seedOwnerInFirm(ctx, t, adminPool, appPool, firmID, "sub-e2e-owner")
	err = zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO role_permissions (firm_id, role_id, permission_key) VALUES ($1, $2, $3)`,
			firmID, ownerRoleID, workflow.AddStockPermission)
		return err
	})
	if err != nil {
		t.Fatalf("grant AddStockPermission to owner: %v", err)
	}

	// The API key is scoped to exactly the one permission the instance
	// creation below needs - proving the whole chain works under a real
	// scope restriction, not just an unscoped/no-restriction key.
	plaintext, _, err := apikey.CreateAPIKey(ctx, appPool, firmID, ownerID, apikey.CreateAPIKeyInput{
		Name: "e2e key", Scopes: []string{workflow.AddStockPermission},
	})
	if err != nil {
		t.Fatalf("create API key: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	hook, err := webhook.CreateWebhook(ctx, appPool, firmID, ownerID, webhook.WebhookInput{
		Name: "e2e hook", URL: server.URL, Events: []string{string(webhook.EventWorkflowInstanceCreated)}, IsActive: true,
	})
	if err != nil {
		t.Fatalf("create webhook: %v", err)
	}

	// Authenticate the request the same way identity.Middleware's
	// Fallback path would for a real HTTP request carrying this API key.
	req := httptest.NewRequest(http.MethodPost, "/api/firms/"+firmID.String()+"/workflow/instances", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	req.SetPathValue("firmID", firmID.String())

	fb := &apikey.Fallback{Pool: appPool}
	resolvedIdentity, scopes, ok := fb.Authenticate(req)
	if !ok {
		t.Fatal("expected the API key to authenticate")
	}
	resolvedUserID, err := identity.ResolveOrCreateUser(ctx, appPool, resolvedIdentity)
	if err != nil {
		t.Fatalf("resolve user from API key identity: %v", err)
	}
	if resolvedUserID != ownerID {
		t.Fatalf("expected the API key to resolve back to its creating owner %s, got %s", ownerID, resolvedUserID)
	}

	scopedCtx := identity.WithScopeRestriction(ctx, scopes)
	instanceID, err := workflow.CreateInstance(scopedCtx, appPool, firmID, resolvedUserID, definitionID, map[string]any{"item": "widget", "quantity": float64(5)})
	if err != nil {
		t.Fatalf("CreateInstance via API key: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var deliveries []webhook.Delivery
	for time.Now().Before(deadline) {
		deliveries, err = webhook.ListDeliveries(ctx, appPool, firmID, ownerID, hook.ID)
		if err == nil && len(deliveries) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(deliveries) == 0 {
		t.Fatal("timed out waiting for the webhook delivery triggered by the API-key-created instance")
	}
	delivery := deliveries[0]
	if !delivery.Success {
		t.Fatalf("expected a successful delivery, got %+v", delivery)
	}
	if delivery.EventType != string(webhook.EventWorkflowInstanceCreated) {
		t.Fatalf("expected event type %q, got %q", webhook.EventWorkflowInstanceCreated, delivery.EventType)
	}
	// webhook_deliveries.payload stores the full signed envelope Dispatch
	// sent ({event, firmId, timestamp, data}, see dispatch.go's own
	// deliverOne) - the event-specific fields workflow.CreateInstance
	// passed to Dispatch live under "data".
	data, ok := delivery.Payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected delivery payload to include a data object, got %v", delivery.Payload)
	}
	if data["instanceId"] != instanceID.String() {
		t.Fatalf("expected delivered payload to reference instance %s, got %v", instanceID, data["instanceId"])
	}
}
