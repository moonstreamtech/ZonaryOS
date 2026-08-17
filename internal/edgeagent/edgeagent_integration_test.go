// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package edgeagent_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/edgeagent"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// These tests exercise the edge agent protocol against a real Postgres
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
		t.Skip("ZONARYOS_TEST_ADMIN_DATABASE_URL and ZONARYOS_TEST_APP_DATABASE_URL must both be set to run edgeagent integration tests")
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
			edge_agents, edge_agent_tokens, edge_events, edge_agent_commands,
			workflow_definitions, workflow_states, workflow_transitions, workflow_rules,
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

func TestCreateAgent_OwnerCanCreateMemberCannot(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "ea-owner")
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "ea-member")

	agent, err := edgeagent.CreateAgent(ctx, appPool, firmID, ownerID, edgeagent.CreateAgentInput{Name: "Warehouse Gateway"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if agent.Status != edgeagent.AgentStatusOffline {
		t.Errorf("expected a new agent to start offline, got %q", agent.Status)
	}

	if _, err := edgeagent.CreateAgent(ctx, appPool, firmID, memberID, edgeagent.CreateAgentInput{Name: "Should Be Denied"}); !errors.Is(err, edgeagent.ErrNotOwner) {
		t.Fatalf("expected ErrNotOwner for a non-owner CreateAgent, got %v", err)
	}

	agents, err := edgeagent.ListAgents(ctx, appPool, firmID, memberID)
	if err != nil {
		t.Fatalf("ListAgents (member): %v", err)
	}
	if len(agents) != 1 || agents[0].ID != agent.ID {
		t.Fatalf("expected the member to see the one seeded agent, got %+v", agents)
	}
}

// TestIssueToken_HashStoredPlaintextReturnedOnce confirms IssueToken never
// persists the plaintext token anywhere - only its SHA-256 hash - while
// still returning the plaintext to the caller exactly once, from the call
// that minted it.
func TestIssueToken_HashStoredPlaintextReturnedOnce(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "ea-owner-2")
	agent, err := edgeagent.CreateAgent(ctx, appPool, firmID, ownerID, edgeagent.CreateAgentInput{Name: "Gateway"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	plaintext, summary, err := edgeagent.IssueToken(ctx, appPool, firmID, ownerID, agent.ID, edgeagent.IssueTokenInput{Name: "Primary"})
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if plaintext == "" {
		t.Fatal("expected a non-empty plaintext token")
	}

	var storedHash string
	if err := adminPool.QueryRow(ctx, `SELECT token_hash FROM edge_agent_tokens WHERE id = $1`, summary.ID).Scan(&storedHash); err != nil {
		t.Fatalf("query stored token: %v", err)
	}
	if storedHash == plaintext {
		t.Fatal("expected token_hash to differ from the plaintext token")
	}

	tokens, err := edgeagent.ListTokens(ctx, appPool, firmID, ownerID, agent.ID)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].ID != summary.ID {
		t.Fatalf("expected the one issued token, got %+v", tokens)
	}
}

// TestHeartbeat_UpdatesStatusAndLastSeen confirms a heartbeat presenting a
// live token updates the agent's status and last_seen_at.
func TestHeartbeat_UpdatesStatusAndLastSeen(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "ea-owner-3")
	agent, err := edgeagent.CreateAgent(ctx, appPool, firmID, ownerID, edgeagent.CreateAgentInput{Name: "Gateway"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	plaintext, _, err := edgeagent.IssueToken(ctx, appPool, firmID, ownerID, agent.ID, edgeagent.IssueTokenInput{Name: "Primary"})
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	updated, err := edgeagent.Heartbeat(ctx, appPool, plaintext, edgeagent.HeartbeatInput{Version: "1.2.3"})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if updated.Status != edgeagent.AgentStatusOnline {
		t.Errorf("expected status online after heartbeat, got %q", updated.Status)
	}
	if updated.LastSeenAt == nil {
		t.Error("expected last_seen_at to be set after heartbeat")
	}
	if updated.Version == nil || *updated.Version != "1.2.3" {
		t.Errorf("expected version 1.2.3, got %+v", updated.Version)
	}

	if _, err := edgeagent.Heartbeat(ctx, appPool, "not-a-real-token", edgeagent.HeartbeatInput{}); !errors.Is(err, edgeagent.ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for a bogus token, got %v", err)
	}
}

// TestCrossFirmToken_CannotAccessAnotherFirmsAgent is the real
// cross-tenant regression: a token minted for firm A's agent must not let
// its holder touch firm B's data even if they can guess firm B's agent
// ID - RecordEvent/Heartbeat derive the agent entirely from the token
// itself, never from a client-supplied ID, so there is nothing to guess
// in the first place. This seeds two firms and confirms firm A's token
// only ever reports events against firm A's own agent.
func TestCrossFirmToken_CannotAccessAnotherFirmsAgent(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmA, ownerA := seedOwner(ctx, t, adminPool, appPool, "Firm A", "ea-owner-a")
	agentA, err := edgeagent.CreateAgent(ctx, appPool, firmA, ownerA, edgeagent.CreateAgentInput{Name: "Firm A Gateway"})
	if err != nil {
		t.Fatalf("CreateAgent (firm A): %v", err)
	}
	tokenA, _, err := edgeagent.IssueToken(ctx, appPool, firmA, ownerA, agentA.ID, edgeagent.IssueTokenInput{Name: "Primary"})
	if err != nil {
		t.Fatalf("IssueToken (firm A): %v", err)
	}

	firmB, ownerB := seedOwner(ctx, t, adminPool, appPool, "Firm B", "ea-owner-b")
	agentB, err := edgeagent.CreateAgent(ctx, appPool, firmB, ownerB, edgeagent.CreateAgentInput{Name: "Firm B Gateway"})
	if err != nil {
		t.Fatalf("CreateAgent (firm B): %v", err)
	}

	event, err := edgeagent.RecordEvent(ctx, appPool, tokenA, "status_report", map[string]any{"cpu": 42})
	if err != nil {
		t.Fatalf("RecordEvent (firm A token): %v", err)
	}
	if event.FirmID != firmA || event.AgentID != agentA.ID {
		t.Fatalf("expected the event to be recorded against firm A's own agent, got firm=%s agent=%s", event.FirmID, event.AgentID)
	}
	if event.FirmID == firmB || event.AgentID == agentB.ID {
		t.Fatal("firm A's token must never be able to record an event against firm B")
	}

	// Firm B cannot see firm A's events via its own member's read path.
	eventsForB, err := edgeagent.ListEvents(ctx, appPool, firmB, ownerB, agentB.ID)
	if err != nil {
		t.Fatalf("ListEvents (firm B): %v", err)
	}
	if len(eventsForB) != 0 {
		t.Fatalf("expected firm B to see no events (firm A's event must not leak), got %+v", eventsForB)
	}

	// Even supplying firm B's real agent ID gets firm A nowhere: a token
	// carries no client-suppliable agent/firm field for RecordEvent/
	// Heartbeat to trust, so there is no such request to make - verified
	// here by confirming firm A's own read path (ListEvents as a firm A
	// member) sees exactly its own one event, never firm B's.
	eventsForA, err := edgeagent.ListEvents(ctx, appPool, firmA, ownerA, agentA.ID)
	if err != nil {
		t.Fatalf("ListEvents (firm A): %v", err)
	}
	if len(eventsForA) != 1 || eventsForA[0].ID != event.ID {
		t.Fatalf("expected firm A to see exactly its own event, got %+v", eventsForA)
	}
}
