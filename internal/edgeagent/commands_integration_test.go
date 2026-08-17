// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package edgeagent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/edgeagent"
)

// TestCreateCommand_OwnerGated confirms CreateCommand is owner-gated,
// same tier as CreateAgent.
func TestCreateCommand_OwnerGated(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "cmd-owner")
	memberID := seedMember(ctx, t, adminPool, appPool, firmID, "cmd-member")
	agent, err := edgeagent.CreateAgent(ctx, appPool, firmID, ownerID, edgeagent.CreateAgentInput{Name: "Gateway"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if _, err := edgeagent.CreateCommand(ctx, appPool, firmID, memberID, agent.ID, edgeagent.CreateCommandInput{CommandType: "reboot"}); !errors.Is(err, edgeagent.ErrNotOwner) {
		t.Fatalf("expected CreateCommand by a non-owner member to fail with ErrNotOwner, got %v", err)
	}

	command, err := edgeagent.CreateCommand(ctx, appPool, firmID, ownerID, agent.ID, edgeagent.CreateCommandInput{CommandType: "reboot", Payload: map[string]any{"delaySeconds": float64(5)}})
	if err != nil {
		t.Fatalf("CreateCommand (owner): %v", err)
	}
	if command.Status != edgeagent.CommandStatusPending {
		t.Errorf("expected a newly created command to be pending, got %q", command.Status)
	}
}

// TestPollCommands_ReturnsOnlyPendingCommandsForTheAuthenticatedAgent
// confirms the poll endpoint's core contract: only THIS agent's pending
// commands come back, an already-delivered/acknowledged command doesn't
// reappear, and another agent's commands are never visible - and that
// polling marks the returned commands 'delivered'.
func TestPollCommands_ReturnsOnlyPendingCommandsForTheAuthenticatedAgent(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "poll-owner")
	agentA, err := edgeagent.CreateAgent(ctx, appPool, firmID, ownerID, edgeagent.CreateAgentInput{Name: "Agent A"})
	if err != nil {
		t.Fatalf("CreateAgent (A): %v", err)
	}
	agentB, err := edgeagent.CreateAgent(ctx, appPool, firmID, ownerID, edgeagent.CreateAgentInput{Name: "Agent B"})
	if err != nil {
		t.Fatalf("CreateAgent (B): %v", err)
	}
	tokenA, _, err := edgeagent.IssueToken(ctx, appPool, firmID, ownerID, agentA.ID, edgeagent.IssueTokenInput{Name: "A's token"})
	if err != nil {
		t.Fatalf("IssueToken (A): %v", err)
	}

	if _, err := edgeagent.CreateCommand(ctx, appPool, firmID, ownerID, agentA.ID, edgeagent.CreateCommandInput{CommandType: "reboot"}); err != nil {
		t.Fatalf("CreateCommand (A, reboot): %v", err)
	}
	if _, err := edgeagent.CreateCommand(ctx, appPool, firmID, ownerID, agentA.ID, edgeagent.CreateCommandInput{CommandType: "update_config"}); err != nil {
		t.Fatalf("CreateCommand (A, update_config): %v", err)
	}
	if _, err := edgeagent.CreateCommand(ctx, appPool, firmID, ownerID, agentB.ID, edgeagent.CreateCommandInput{CommandType: "reboot"}); err != nil {
		t.Fatalf("CreateCommand (B, reboot): %v", err)
	}

	commands, err := edgeagent.PollCommands(ctx, appPool, tokenA)
	if err != nil {
		t.Fatalf("PollCommands: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("expected exactly agent A's 2 pending commands, got %d: %+v", len(commands), commands)
	}
	for _, c := range commands {
		if c.AgentID != agentA.ID {
			t.Errorf("expected every returned command to belong to agent A, got agentId %s", c.AgentID)
		}
		if c.Status != edgeagent.CommandStatusDelivered {
			t.Errorf("expected a polled command's returned status to be 'delivered', got %q", c.Status)
		}
	}

	// A second poll must NOT return the same (now-delivered) commands
	// again.
	secondPoll, err := edgeagent.PollCommands(ctx, appPool, tokenA)
	if err != nil {
		t.Fatalf("PollCommands (second): %v", err)
	}
	if len(secondPoll) != 0 {
		t.Errorf("expected a second poll to return no commands (already delivered), got %d", len(secondPoll))
	}
}

// TestAckCommand_UpdatesStatusAndIsScopedToTheOwningAgent confirms Ack
// transitions a command to 'acknowledged', and that an agent cannot
// acknowledge another agent's command (its own token only ever resolves
// its own AgentID/FirmID - see AckCommand's own doc comment).
func TestAckCommand_UpdatesStatusAndIsScopedToTheOwningAgent(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()

	firmID, ownerID := seedOwner(ctx, t, adminPool, appPool, "Firm A", "ack-owner")
	agentA, err := edgeagent.CreateAgent(ctx, appPool, firmID, ownerID, edgeagent.CreateAgentInput{Name: "Agent A"})
	if err != nil {
		t.Fatalf("CreateAgent (A): %v", err)
	}
	agentB, err := edgeagent.CreateAgent(ctx, appPool, firmID, ownerID, edgeagent.CreateAgentInput{Name: "Agent B"})
	if err != nil {
		t.Fatalf("CreateAgent (B): %v", err)
	}
	tokenA, _, err := edgeagent.IssueToken(ctx, appPool, firmID, ownerID, agentA.ID, edgeagent.IssueTokenInput{Name: "A's token"})
	if err != nil {
		t.Fatalf("IssueToken (A): %v", err)
	}
	tokenB, _, err := edgeagent.IssueToken(ctx, appPool, firmID, ownerID, agentB.ID, edgeagent.IssueTokenInput{Name: "B's token"})
	if err != nil {
		t.Fatalf("IssueToken (B): %v", err)
	}

	command, err := edgeagent.CreateCommand(ctx, appPool, firmID, ownerID, agentA.ID, edgeagent.CreateCommandInput{CommandType: "reboot"})
	if err != nil {
		t.Fatalf("CreateCommand: %v", err)
	}

	if _, err := edgeagent.AckCommand(ctx, appPool, tokenB, command.ID); !errors.Is(err, edgeagent.ErrCommandNotFound) {
		t.Fatalf("expected agent B acknowledging agent A's command to fail with ErrCommandNotFound, got %v", err)
	}

	acked, err := edgeagent.AckCommand(ctx, appPool, tokenA, command.ID)
	if err != nil {
		t.Fatalf("AckCommand (A, own command): %v", err)
	}
	if acked.Status != edgeagent.CommandStatusAcknowledged {
		t.Errorf("expected status 'acknowledged', got %q", acked.Status)
	}
	if acked.AcknowledgedAt == nil {
		t.Error("expected acknowledgedAt to be set")
	}
}
