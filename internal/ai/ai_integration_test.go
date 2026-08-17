// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/ai"
	"github.com/moonstreamtech/ZonaryOS/internal/notification"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise internal/ai against a real Postgres instance - RLS
// isolation and permission enforcement can't be meaningfully faked, same
// convention every other package's integration tests in this codebase
// follow (see internal/asset/asset_integration_test.go). Skipped unless
// both ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL
// are set. No test in this file makes a real network call to an AI
// vendor - every test that needs a provider swaps ai.ProviderFactory for
// a mock, per Part 5's own "mock the AI provider" test requirement.

func testPools(t *testing.T) (adminDSN, appDSN string) {
	t.Helper()
	adminDSN = os.Getenv("ZONARYOS_TEST_ADMIN_DATABASE_URL")
	appDSN = os.Getenv("ZONARYOS_TEST_APP_DATABASE_URL")
	if adminDSN == "" || appDSN == "" {
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run ai integration tests")
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
			ai_configurations, notifications, accounts, journal_entries, journal_lines,
			audit_log, permissions CASCADE
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

func testEncryptor(t *testing.T) *ai.Encryptor {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate encryptor key: %v", err)
	}
	enc, err := ai.NewEncryptor(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return enc
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

// mockProvider implements ai.Provider without ever making a network call.
type mockProvider struct {
	response string
	err      error
	prompts  *[]string // if set, every user prompt this provider is Complete()-d with is appended here
}

func (m mockProvider) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	if m.prompts != nil {
		*m.prompts = append(*m.prompts, userPrompt)
	}
	return m.response, m.err
}

func withMockProvider(t *testing.T, p ai.Provider) {
	t.Helper()
	original := ai.ProviderFactory
	ai.ProviderFactory = func(name ai.ProviderName, model, apiKey string) (ai.Provider, error) {
		return p, nil
	}
	t.Cleanup(func() { ai.ProviderFactory = original })
}

func createTestConfig(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, encryptor *ai.Encryptor, firmID, userID uuid.UUID) ai.Configuration {
	t.Helper()
	cfg, err := ai.CreateConfig(ctx, appPool, encryptor, firmID, userID, ai.CreateConfigInput{
		Provider: ai.ProviderAnthropic, Model: "claude-test", APIKey: "sk-ant-test-key",
	})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	return cfg
}

func TestSuggestWorkflow_ValidSuggestion_PassesThrough(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	encryptor := testEncryptor(t)

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "suggest-workflow-owner")
	createTestConfig(ctx, t, appPool, encryptor, firmID, userID)

	validResponse := `{"key":"order_to_fulfillment","name":"Order to Fulfillment","createPermission":{"key":"orders.create","description":"Create an order"},"states":[{"key":"pending","name":"Pending","isInitial":true,"isTerminal":false},{"key":"done","name":"Done","isInitial":false,"isTerminal":true}],"transitions":[{"fromStateKey":"pending","toStateKey":"done","actionKey":"complete","name":"Complete","permission":{"key":"orders.complete","description":"Complete the order"}}]}`
	withMockProvider(t, mockProvider{response: validResponse})

	suggestion, err := ai.SuggestWorkflow(ctx, appPool, encryptor, firmID, userID, "when an order is placed, mark it done once fulfilled")
	if err != nil {
		t.Fatalf("SuggestWorkflow: %v", err)
	}
	if suggestion.Key != "order_to_fulfillment" {
		t.Fatalf("expected key %q, got %q", "order_to_fulfillment", suggestion.Key)
	}
	if len(suggestion.States) != 2 || len(suggestion.Transitions) != 1 {
		t.Fatalf("expected 2 states and 1 transition to pass through, got %d states, %d transitions", len(suggestion.States), len(suggestion.Transitions))
	}
}

func TestSuggestWorkflow_InvalidJSON_ReturnsInvalidSuggestion(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	encryptor := testEncryptor(t)

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "suggest-workflow-invalid-owner")
	createTestConfig(ctx, t, appPool, encryptor, firmID, userID)

	withMockProvider(t, mockProvider{response: "this is not JSON at all"})

	_, err := ai.SuggestWorkflow(ctx, appPool, encryptor, firmID, userID, "some process")
	if !errors.Is(err, ai.ErrInvalidAISuggestion) {
		t.Fatalf("expected ErrInvalidAISuggestion, got %v", err)
	}
}

func TestSuggestWorkflow_InvalidSpec_ReturnsInvalidSuggestion(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	encryptor := testEncryptor(t)

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "suggest-workflow-badspec-owner")
	createTestConfig(ctx, t, appPool, encryptor, firmID, userID)

	// Valid JSON, but structurally invalid: two initial states.
	badSpec := `{"key":"bad","name":"Bad","createPermission":{"key":"bad.create","description":"x"},"states":[{"key":"a","name":"A","isInitial":true},{"key":"b","name":"B","isInitial":true}],"transitions":[]}`
	withMockProvider(t, mockProvider{response: badSpec})

	_, err := ai.SuggestWorkflow(ctx, appPool, encryptor, firmID, userID, "some process")
	if !errors.Is(err, ai.ErrInvalidAISuggestion) {
		t.Fatalf("expected ErrInvalidAISuggestion, got %v", err)
	}
}

func TestSuggestWorkflowAndReport_NoActiveConfig_ReturnsErrNoActiveConfig(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	encryptor := testEncryptor(t)

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "no-config-owner")
	// Deliberately no CreateConfig call - Part 5's opt-in contract: every
	// /ai/* call must behave as if the feature doesn't exist at all.

	if _, err := ai.SuggestWorkflow(ctx, appPool, encryptor, firmID, userID, "some process"); !errors.Is(err, ai.ErrNoActiveConfig) {
		t.Fatalf("SuggestWorkflow: expected ErrNoActiveConfig, got %v", err)
	}
	if _, err := ai.SuggestReport(ctx, appPool, encryptor, firmID, userID, "how many sales this month?"); !errors.Is(err, ai.ErrNoActiveConfig) {
		t.Fatalf("SuggestReport: expected ErrNoActiveConfig, got %v", err)
	}
}

func TestSuggestReport_ValidSuggestion_PassesThrough(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	encryptor := testEncryptor(t)

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "suggest-report-owner")
	createTestConfig(ctx, t, appPool, encryptor, firmID, userID)

	validResponse := `{"entity":"invoices","metrics":[{"name":"*","aggregation":"count"}]}`
	withMockProvider(t, mockProvider{response: validResponse})

	spec, err := ai.SuggestReport(ctx, appPool, encryptor, firmID, userID, "how many invoices did we issue?")
	if err != nil {
		t.Fatalf("SuggestReport: %v", err)
	}
	if spec.Entity != "invoices" || len(spec.Metrics) != 1 {
		t.Fatalf("expected entity=invoices with 1 metric, got %+v", spec)
	}
}

// seedChartOfAccounts seeds firmID's chart of accounts once - callers that
// post more than one journal entry for the same firm (seedJournalActivity)
// must call this exactly once per firm, since a second call would try to
// insert the same account codes again and hit accounts' own
// UNIQUE (firm_id, code) constraint.
func seedChartOfAccounts(ctx context.Context, t *testing.T, appPool *pgxpool.Pool, firmID uuid.UUID) {
	t.Helper()
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return accounting.SeedDefaultChartOfAccountsTx(ctx, tx, firmID, accounting.SeedChartOptions{Sells: true})
	})
	if err != nil {
		t.Fatalf("seed chart of accounts: %v", err)
	}
}

// seedJournalActivity posts one balanced journal entry (cash debit /
// sales revenue credit) for firmID, then backdates it to daysAgo days in
// the past - the same "insert via the normal path, then UPDATE
// posted_at directly" pattern internal/accounting's own integration tests
// use to get a real, distinct posted_at to filter/aggregate on. Callers
// must seed the chart of accounts (seedChartOfAccounts) once beforehand.
func seedJournalActivity(ctx context.Context, t *testing.T, adminPool, appPool *pgxpool.Pool, firmID, userID uuid.UUID, description, amount string, daysAgo int) {
	t.Helper()

	var entryID uuid.UUID
	err := zdb.WithFirmContext(ctx, appPool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		entryID, err = accounting.PostJournalEntryTx(ctx, tx, firmID, userID, description, "", nil, []accounting.LineInput{
			{AccountCode: accounting.CashAccountCode, Side: accounting.SideDebit, Amount: amount},
			{AccountCode: accounting.SalesRevenueAccountCode, Side: accounting.SideCredit, Amount: amount},
		})
		return err
	})
	if err != nil {
		t.Fatalf("post journal entry: %v", err)
	}

	if _, err := adminPool.Exec(ctx, `UPDATE journal_entries SET posted_at = now() - ($1 * INTERVAL '1 day') WHERE id = $2`, daysAgo, entryID); err != nil {
		t.Fatalf("backdate journal entry: %v", err)
	}
}

// TestProcessAnomalyChecks_NotifiesOwnersWhenFlagged_AndPromptExcludesPII
// is this batch's own PII-firewall test requirement: it seeds a journal
// entry whose description contains a marker string that would never be
// allowed to reach the AI provider, captures the exact prompt text the
// mock provider receives, and asserts that marker is absent while the
// aggregated numeric total IS present.
func TestProcessAnomalyChecks_NotifiesOwnersWhenFlagged_AndPromptExcludesPII(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	encryptor := testEncryptor(t)

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "anomaly-owner")
	createTestConfig(ctx, t, appPool, encryptor, firmID, userID)

	const piiMarker = "CUSTOMER_ACME_CORP_SECRET_DEAL"
	seedChartOfAccounts(ctx, t, appPool, firmID)
	seedJournalActivity(ctx, t, adminPool, appPool, firmID, userID, piiMarker, "5000.0000", 2)
	seedJournalActivity(ctx, t, adminPool, appPool, firmID, userID, "prior period baseline", "100.0000", 10)

	var capturedPrompts []string
	withMockProvider(t, mockProvider{response: "Revenue for account 4000 jumped unusually compared to the prior week.", prompts: &capturedPrompts})

	ai.ProcessAnomalyChecks(ctx, appPool, encryptor)

	if len(capturedPrompts) != 1 {
		t.Fatalf("expected exactly 1 provider call, got %d", len(capturedPrompts))
	}
	prompt := capturedPrompts[0]
	if strings.Contains(prompt, piiMarker) {
		t.Fatalf("PII firewall violation: prompt sent to AI provider contained the journal entry description %q:\n%s", piiMarker, prompt)
	}
	if !strings.Contains(prompt, "5000.0000") && !strings.Contains(prompt, "5000") {
		t.Fatalf("expected the aggregated current-period total to appear in the prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, accounting.SalesRevenueAccountCode) {
		t.Fatalf("expected the account code to appear in the prompt:\n%s", prompt)
	}

	result, err := notification.ListForUser(ctx, appPool, firmID, userID, notification.ListOptions{})
	if err != nil {
		t.Fatalf("ListForUser notifications: %v", err)
	}
	found := false
	for _, n := range result.Notifications {
		if n.Type == "ai_anomaly_flagged" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected an ai_anomaly_flagged notification to have been created for the firm owner")
	}
}

func TestProcessAnomalyChecks_NothingUnusual_NoNotification(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	encryptor := testEncryptor(t)

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "anomaly-quiet-owner")
	createTestConfig(ctx, t, appPool, encryptor, firmID, userID)
	seedChartOfAccounts(ctx, t, appPool, firmID)
	seedJournalActivity(ctx, t, adminPool, appPool, firmID, userID, "routine sale", "100.0000", 2)

	withMockProvider(t, mockProvider{response: "NOTHING_UNUSUAL"})

	ai.ProcessAnomalyChecks(ctx, appPool, encryptor)

	result, err := notification.ListForUser(ctx, appPool, firmID, userID, notification.ListOptions{})
	if err != nil {
		t.Fatalf("ListForUser notifications: %v", err)
	}
	for _, n := range result.Notifications {
		if n.Type == "ai_anomaly_flagged" {
			t.Fatal("expected no ai_anomaly_flagged notification when the AI response is NOTHING_UNUSUAL")
		}
	}
}

func TestProcessAnomalyChecks_NoActiveConfig_SkipsFirm(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	encryptor := testEncryptor(t)

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "anomaly-noconfig-owner")
	seedChartOfAccounts(ctx, t, appPool, firmID)
	seedJournalActivity(ctx, t, adminPool, appPool, firmID, userID, "routine sale", "100.0000", 2)
	// No CreateConfig call.

	called := false
	withMockProvider(t, mockProvider{response: "NOTHING_UNUSUAL", prompts: nil})
	ai.ProviderFactory = func(name ai.ProviderName, model, apiKey string) (ai.Provider, error) {
		called = true
		return mockProvider{response: "NOTHING_UNUSUAL"}, nil
	}
	t.Cleanup(func() { ai.ProviderFactory = ai.NewProvider })

	ai.ProcessAnomalyChecks(ctx, appPool, encryptor)

	if called {
		t.Fatal("expected the AI provider to never be called for a firm with no active AI configuration")
	}
}
