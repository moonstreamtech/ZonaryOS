// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/crm"
	"github.com/moonstreamtech/ZonaryOS/internal/cryptutil"
	"github.com/moonstreamtech/ZonaryOS/internal/integration"
	"github.com/moonstreamtech/ZonaryOS/internal/inventory"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise the data pipeline/ETL/system integrations batch
// against a real Postgres instance - RLS isolation and permission
// enforcement can't be meaningfully faked, same convention as every other
// package's integration tests in this codebase (see
// internal/webhook/webhook_integration_test.go). Skipped unless both
// ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL are
// set. See docs/DEVELOPMENT.md.

func testDSNs(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run integration package integration tests")
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
			integration_connectors, integration_mappings, integration_sync_logs,
			products, customers, audit_log, permissions CASCADE
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

func testEncryptor(t *testing.T) *cryptutil.Encryptor {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	enc, err := cryptutil.NewEncryptor(base64.StdEncoding.EncodeToString(key), "TEST_KEY")
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return enc
}

// TestConnectorCRUD_And_TestConnection covers Part 5's own explicit
// "connector CRUD + test-connection for http_webhook" requirement.
func TestConnectorCRUD_And_TestConnection(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Connector CRUD Firm", "owner-connector-crud")
	enc := testEncryptor(t)

	pingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer pingServer.Close()

	connector, err := integration.CreateConnector(ctx, appPool, enc, firmID, userID, integration.ConnectorInput{
		Name: "Partner Webhook", Type: integration.ConnectorHTTPWebhook,
		Config:   map[string]any{"url": pingServer.URL, "apiKey": "sk-super-secret-1234"},
		IsActive: true,
	})
	if err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}
	if connector.Config["apiKey"] == "sk-super-secret-1234" {
		t.Fatal("expected apiKey to be masked in the returned Connector, got the plaintext")
	}
	if connector.Config["apiKey"] != "...1234" {
		t.Fatalf("expected masked apiKey \"...1234\", got %v", connector.Config["apiKey"])
	}

	if err := integration.TestConnection(ctx, appPool, enc, firmID, userID, connector.ID); err != nil {
		t.Fatalf("TestConnection: expected success against a live ping server, got %v", err)
	}

	connectors, err := integration.ListConnectors(ctx, appPool, enc, firmID, userID)
	if err != nil {
		t.Fatalf("ListConnectors: %v", err)
	}
	if len(connectors) != 1 || connectors[0].ID != connector.ID {
		t.Fatalf("expected exactly the one created connector, got %+v", connectors)
	}

	updated, err := integration.UpdateConnector(ctx, appPool, enc, firmID, userID, connector.ID, integration.ConnectorInput{
		Name: "Partner Webhook (renamed)", Type: integration.ConnectorHTTPWebhook,
		Config: map[string]any{"url": pingServer.URL}, IsActive: false,
	})
	if err != nil {
		t.Fatalf("UpdateConnector: %v", err)
	}
	if updated.Name != "Partner Webhook (renamed)" || updated.IsActive {
		t.Fatalf("update did not take effect: %+v", updated)
	}

	if err := integration.DeleteConnector(ctx, appPool, firmID, userID, connector.ID); err != nil {
		t.Fatalf("DeleteConnector: %v", err)
	}
	remaining, err := integration.ListConnectors(ctx, appPool, enc, firmID, userID)
	if err != nil {
		t.Fatalf("ListConnectors after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected no connectors after delete, got %+v", remaining)
	}
}

// TestTestConnection_NonHTTPWebhookNotImplemented covers Part 1's own "any
// other connector type returns 'not implemented' for now" contract.
func TestTestConnection_NonHTTPWebhookNotImplemented(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "FTP Not Implemented Firm", "owner-ftp")
	enc := testEncryptor(t)

	connector, err := integration.CreateConnector(ctx, appPool, enc, firmID, userID, integration.ConnectorInput{
		Name: "Legacy FTP", Type: integration.ConnectorFTP, Config: map[string]any{"url": "ftp://example.com"}, IsActive: true,
	})
	if err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}

	if err := integration.TestConnection(ctx, appPool, enc, firmID, userID, connector.ID); err != integration.ErrConnectionTestNotImplemented {
		t.Fatalf("expected ErrConnectionTestNotImplemented for an ftp connector, got %v", err)
	}
}

// waitForSyncLog polls integration_sync_logs for connectorID until one
// reaches a finished (non-'running') status, or the deadline passes -
// DispatchOutbound's own async design means the test can't assert
// synchronously right after calling it.
func waitForFinishedSyncLog(t *testing.T, appPool *pgxpool.Pool, firmID, userID, connectorID uuid.UUID) integration.SyncLog {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		logs, err := integration.ListSyncLogs(context.Background(), appPool, firmID, userID, connectorID)
		if err == nil {
			for _, l := range logs {
				if l.Status != integration.SyncStatusRunning {
					return l
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for a finished sync log")
	return integration.SyncLog{}
}

// TestDispatchOutbound_DeliversMappedFieldsToMockTarget covers Part 5's
// own "outbound push: mock HTTP target, confirm mapped fields delivered"
// requirement, exercising DispatchOutbound end to end against a real
// connector + outbound mapping.
func TestDispatchOutbound_DeliversMappedFieldsToMockTarget(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Outbound Push Firm", "owner-outbound")
	enc := testEncryptor(t)
	integration.SetEncryptor(enc)
	t.Cleanup(func() { integration.SetEncryptor(nil) })

	var receivedBody []byte
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	connector, err := integration.CreateConnector(ctx, appPool, enc, firmID, userID, integration.ConnectorInput{
		Name: "Outbound Target", Type: integration.ConnectorHTTPWebhook,
		Config: map[string]any{"url": target.URL, "secret": "test-secret"}, IsActive: true,
	})
	if err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}

	if _, err := integration.CreateMapping(ctx, appPool, firmID, userID, integration.MappingInput{
		ConnectorID: connector.ID, SourceEntity: "products", TargetEntity: "partner_products",
		FieldMappings: []integration.FieldMapping{
			{SourceField: "name", TargetField: "productName", Transform: integration.TransformChain{"trim", "uppercase"}},
			{SourceField: "sku", TargetField: "sku"},
		},
		SyncDirection: integration.SyncOutbound, IsActive: true,
	}); err != nil {
		t.Fatalf("CreateMapping: %v", err)
	}

	integration.DispatchOutbound(appPool, firmID, "products", map[string]any{
		"name": "  widget  ", "sku": "SKU-1",
	})

	log := waitForFinishedSyncLog(t, appPool, firmID, userID, connector.ID)
	if log.Status != integration.SyncStatusSuccess {
		t.Fatalf("expected a successful sync log, got %+v", log)
	}

	var decoded map[string]any
	if err := json.Unmarshal(receivedBody, &decoded); err != nil {
		t.Fatalf("delivered body is not valid JSON: %v (body: %s)", err, receivedBody)
	}
	data, ok := decoded["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected a \"data\" object in the delivered body, got %v", decoded)
	}
	if data["productName"] != "WIDGET" {
		t.Fatalf("expected mapped+transformed productName \"WIDGET\", got %v", data["productName"])
	}
	if data["sku"] != "SKU-1" {
		t.Fatalf("expected mapped sku \"SKU-1\", got %v", data["sku"])
	}
	if decoded["entity"] != "partner_products" {
		t.Fatalf("expected entity \"partner_products\", got %v", decoded["entity"])
	}
}

// TestProcessInboundSyncs_CreatesProductFromMockSource covers Part 5's own
// "inbound pull: mock HTTP source returning JSON, confirm product created
// with transformed field values" requirement.
func TestProcessInboundSyncs_CreatesProductFromMockSource(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Inbound Pull Firm", "owner-inbound")
	enc := testEncryptor(t)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"itemName":"  gadget  ","itemSku":"SKU-INBOUND-1","itemPrice":"10"}]`))
	}))
	defer source.Close()

	connector, err := integration.CreateConnector(ctx, appPool, enc, firmID, userID, integration.ConnectorInput{
		Name: "Partner Feed", Type: integration.ConnectorHTTPWebhook,
		Config: map[string]any{"url": source.URL, "pollIntervalMinutes": float64(1)}, IsActive: true,
	})
	if err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}

	if _, err := integration.CreateMapping(ctx, appPool, firmID, userID, integration.MappingInput{
		ConnectorID: connector.ID, SourceEntity: "partner_products", TargetEntity: "products",
		FieldMappings: []integration.FieldMapping{
			{SourceField: "itemName", TargetField: "name", Transform: integration.TransformChain{"trim", "uppercase"}},
			{SourceField: "itemSku", TargetField: "sku"},
			{SourceField: "itemPrice", TargetField: "unitPrice", Transform: integration.TransformChain{"multiply:1.2"}},
		},
		SyncDirection: integration.SyncInbound, IsActive: true,
	}); err != nil {
		t.Fatalf("CreateMapping: %v", err)
	}

	integration.ProcessInboundSyncs(ctx, appPool, enc)

	products, err := inventory.ListProducts(ctx, appPool, firmID, userID, inventory.ListProductsOptions{})
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	var created *inventory.Product
	for i := range products {
		if products[i].SKU == "SKU-INBOUND-1" {
			created = &products[i]
		}
	}
	if created == nil {
		t.Fatalf("expected a product with SKU %q to have been created, got %+v", "SKU-INBOUND-1", products)
	}
	if created.Name != "GADGET" {
		t.Fatalf("expected transformed name \"GADGET\", got %q", created.Name)
	}
	if created.UnitPrice == nil {
		t.Fatal("expected a non-nil unitPrice")
	} else if gotPrice, err := strconv.ParseFloat(*created.UnitPrice, 64); err != nil || gotPrice != 12 {
		t.Fatalf("expected transformed unitPrice 12 (10 * 1.2), got %q (parse err: %v)", *created.UnitPrice, err)
	}

	logs, err := integration.ListSyncLogs(ctx, appPool, firmID, userID, connector.ID)
	if err != nil {
		t.Fatalf("ListSyncLogs: %v", err)
	}
	if len(logs) != 1 || logs[0].RecordsProcessed != 1 || logs[0].RecordsFailed != 0 {
		t.Fatalf("expected exactly one successful sync log with 1 record processed, got %+v", logs)
	}
}

// TestProcessInboundSyncs_UpdatesExistingCustomerByEmail covers the
// idempotency approach's "update" half - a second poll against a source
// with a changed field updates the SAME customer (matched by its natural
// key, email) rather than creating a duplicate.
func TestProcessInboundSyncs_UpdatesExistingCustomerByEmail(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Inbound Update Firm", "owner-inbound-update")
	enc := testEncryptor(t)

	if _, err := crm.CreateCustomer(ctx, appPool, firmID, userID, crm.CreateCustomerInput{
		Name: "Old Name", Email: "customer@example.com", Currency: "USD",
	}); err != nil {
		t.Fatalf("seed existing customer: %v", err)
	}

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"custEmail":"customer@example.com","custName":"New Name"}]`))
	}))
	defer source.Close()

	connector, err := integration.CreateConnector(ctx, appPool, enc, firmID, userID, integration.ConnectorInput{
		Name: "Partner CRM Feed", Type: integration.ConnectorHTTPWebhook,
		Config: map[string]any{"url": source.URL, "pollIntervalMinutes": float64(1)}, IsActive: true,
	})
	if err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}
	if _, err := integration.CreateMapping(ctx, appPool, firmID, userID, integration.MappingInput{
		ConnectorID: connector.ID, SourceEntity: "partner_customers", TargetEntity: "customers",
		FieldMappings: []integration.FieldMapping{
			{SourceField: "custEmail", TargetField: "email"},
			{SourceField: "custName", TargetField: "name"},
		},
		SyncDirection: integration.SyncInbound, IsActive: true,
	}); err != nil {
		t.Fatalf("CreateMapping: %v", err)
	}

	integration.ProcessInboundSyncs(ctx, appPool, enc)

	customers, err := crm.ListCustomers(ctx, appPool, firmID, userID, crm.ListCustomersOptions{})
	if err != nil {
		t.Fatalf("ListCustomers: %v", err)
	}
	var matches []crm.Customer
	for _, c := range customers {
		if c.Email != nil && *c.Email == "customer@example.com" {
			matches = append(matches, c)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one customer with email customer@example.com (updated, not duplicated), got %d: %+v", len(matches), matches)
	}
	if matches[0].Name != "New Name" {
		t.Fatalf("expected the existing customer's name to be updated to \"New Name\", got %q", matches[0].Name)
	}
}

// TestProcessInboundSyncs_UnsupportedConnectorTypeReturnsClearError covers
// Part 3's own "no silent failures" requirement for ftp/sftp/database -
// the sync log records ErrConnectorTypeNotSupported rather than silently
// doing nothing.
func TestProcessInboundSyncs_UnsupportedConnectorTypeReturnsClearError(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Unsupported Connector Type Firm", "owner-unsupported")
	enc := testEncryptor(t)

	connector, err := integration.CreateConnector(ctx, appPool, enc, firmID, userID, integration.ConnectorInput{
		Name: "Legacy Database Feed", Type: integration.ConnectorDatabase,
		Config: map[string]any{"pollIntervalMinutes": float64(1)}, IsActive: true,
	})
	if err != nil {
		t.Fatalf("CreateConnector: %v", err)
	}
	if _, err := integration.CreateMapping(ctx, appPool, firmID, userID, integration.MappingInput{
		ConnectorID: connector.ID, SourceEntity: "partner_products", TargetEntity: "products",
		FieldMappings: []integration.FieldMapping{{SourceField: "a", TargetField: "sku"}},
		SyncDirection: integration.SyncInbound, IsActive: true,
	}); err != nil {
		t.Fatalf("CreateMapping: %v", err)
	}

	integration.ProcessInboundSyncs(ctx, appPool, enc)

	log := waitForFinishedSyncLog(t, appPool, firmID, userID, connector.ID)
	if log.Status != integration.SyncStatusFailed {
		t.Fatalf("expected a failed sync log for an unsupported connector type, got %+v", log)
	}
	if len(log.ErrorLog) == 0 {
		t.Fatal("expected a non-empty error log describing the unsupported connector type")
	}
}
