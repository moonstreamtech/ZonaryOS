// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Part 2 of the NATS JetStream/Edge Agent completion batch: until now the
// server could only ever receive FROM an agent (heartbeat, RecordEvent) -
// this file adds the other direction, a firm queuing a command an agent
// then polls for and acknowledges. Poll-based, not WebSocket/long-poll
// push (this batch's own scope boundary) - the agent calls GET
// /api/edge/commands on its own schedule, the same "the agent polls, the
// server never pushes" posture this package's own doc comment already
// establishes for heartbeat/events.
package edgeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const commandAuditEntityType = "edge_agent_command"

const createCommandAuditAction = "create"

// CommandStatus is edge_agent_commands.status's closed vocabulary - a
// real DB CHECK constraint (migrations/0034_nats_edge_agent_resilience.up.sql).
type CommandStatus string

const (
	CommandStatusPending      CommandStatus = "pending"
	CommandStatusDelivered    CommandStatus = "delivered"
	CommandStatusAcknowledged CommandStatus = "acknowledged"
	CommandStatusFailed       CommandStatus = "failed"
)

// Command is one edge_agent_commands row.
type Command struct {
	ID             uuid.UUID
	FirmID         uuid.UUID
	AgentID        uuid.UUID
	CommandType    string
	Payload        map[string]any
	Status         CommandStatus
	CreatedAt      time.Time
	DeliveredAt    *time.Time
	AcknowledgedAt *time.Time
}

const commandColumns = "id, firm_id, agent_id, command_type, payload, status, created_at, delivered_at, acknowledged_at"

func scanCommand(row pgx.Row) (Command, error) {
	var c Command
	var statusRaw string
	var payloadRaw []byte
	if err := row.Scan(&c.ID, &c.FirmID, &c.AgentID, &c.CommandType, &payloadRaw, &statusRaw, &c.CreatedAt, &c.DeliveredAt, &c.AcknowledgedAt); err != nil {
		return Command{}, err
	}
	c.Status = CommandStatus(statusRaw)
	if err := json.Unmarshal(payloadRaw, &c.Payload); err != nil {
		return Command{}, fmt.Errorf("unmarshal command payload: %w", err)
	}
	return c, nil
}

// CreateCommandInput is CreateCommand's request shape.
type CreateCommandInput struct {
	CommandType string
	Payload     map[string]any
}

// CreateCommand enqueues a new, 'pending' command for agentID within
// firmID. Owner-gated: sending a command to a firm's own hardware is a
// structural firm decision, the same tier as CreateAgent.
func CreateCommand(ctx context.Context, pool *pgxpool.Pool, firmID, userID, agentID uuid.UUID, input CreateCommandInput) (Command, error) {
	commandType := strings.TrimSpace(input.CommandType)
	if commandType == "" {
		return Command{}, fmt.Errorf("%w: commandType must not be empty", ErrInvalidCommand)
	}
	payload := input.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return Command{}, fmt.Errorf("marshal command payload: %w", err)
	}

	var command Command
	err = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}
		isOwner, err := permission.IsOwner(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isOwner {
			return ErrNotOwner
		}

		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM edge_agents WHERE id = $1 AND firm_id = $2)`, agentID, firmID).Scan(&exists); err != nil {
			return fmt.Errorf("check edge agent: %w", err)
		}
		if !exists {
			return ErrAgentNotFound
		}

		command = Command{FirmID: firmID, AgentID: agentID, CommandType: commandType, Payload: payload, Status: CommandStatusPending}
		if err := tx.QueryRow(ctx, `
			INSERT INTO edge_agent_commands (firm_id, agent_id, command_type, payload)
			VALUES ($1, $2, $3, $4)
			RETURNING id, created_at
		`, firmID, agentID, commandType, payloadJSON).Scan(&command.ID, &command.CreatedAt); err != nil {
			return fmt.Errorf("insert edge agent command: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, command.ID, commandAuditEntityType, createCommandAuditAction, map[string]any{
			"agentId":     agentID.String(),
			"commandType": commandType,
		})
	})
	if err != nil {
		return Command{}, err
	}
	return command, nil
}

// ListCommands lists agentID's commands within firmID, most recent
// first, capped at 200 rows - the command history section of the edge
// agent detail page. Member-gated read, same tier as ListEvents.
func ListCommands(ctx context.Context, pool *pgxpool.Pool, firmID, userID, agentID uuid.UUID) ([]Command, error) {
	var commands []Command
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM edge_agents WHERE id = $1 AND firm_id = $2)`, agentID, firmID).Scan(&exists); err != nil {
			return fmt.Errorf("check edge agent: %w", err)
		}
		if !exists {
			return ErrAgentNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT `+commandColumns+`
			FROM edge_agent_commands WHERE agent_id = $1 AND firm_id = $2
			ORDER BY created_at DESC LIMIT 200
		`, agentID, firmID)
		if err != nil {
			return fmt.Errorf("list edge agent commands: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCommand(rows)
			if err != nil {
				return fmt.Errorf("scan edge agent command: %w", err)
			}
			commands = append(commands, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return commands, nil
}

// PollCommands is GET /api/edge/commands' business logic: returns every
// 'pending' command for the agent identified by rawToken, and marks each
// one returned 'delivered' in the same transaction - "delivered" means
// "the agent has now seen this command", not "the agent has run it" (see
// AckCommand for that). Same "identity comes only from the token"
// guarantee as Heartbeat/RecordEvent.
func PollCommands(ctx context.Context, pool *pgxpool.Pool, rawToken string) ([]Command, error) {
	var commands []Command
	err := withTokenTx(ctx, pool, rawToken, func(ctx context.Context, tx pgx.Tx, auth AuthenticatedAgent) error {
		rows, err := tx.Query(ctx, `
			SELECT `+commandColumns+`
			FROM edge_agent_commands
			WHERE agent_id = $1 AND firm_id = $2 AND status = $3
			ORDER BY created_at
		`, auth.AgentID, auth.FirmID, string(CommandStatusPending))
		if err != nil {
			return fmt.Errorf("list pending edge agent commands: %w", err)
		}
		var ids []uuid.UUID
		for rows.Next() {
			c, err := scanCommand(rows)
			if err != nil {
				rows.Close()
				return fmt.Errorf("scan edge agent command: %w", err)
			}
			commands = append(commands, c)
			ids = append(ids, c.ID)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()

		if len(ids) == 0 {
			return nil
		}
		if _, err := tx.Exec(ctx, `
			UPDATE edge_agent_commands SET status = $1, delivered_at = now() WHERE id = ANY($2)
		`, string(CommandStatusDelivered), ids); err != nil {
			return fmt.Errorf("mark edge agent commands delivered: %w", err)
		}
		for i := range commands {
			commands[i].Status = CommandStatusDelivered
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return commands, nil
}

// AckCommand marks commandID 'acknowledged' - the agent's own report
// that it has finished executing the command. Same "identity comes only
// from the token" guarantee as Heartbeat/RecordEvent: an agent can only
// ever acknowledge its OWN commands (the WHERE clause below is scoped to
// auth.AgentID/auth.FirmID, never a client-supplied firm/agent id).
func AckCommand(ctx context.Context, pool *pgxpool.Pool, rawToken string, commandID uuid.UUID) (Command, error) {
	var command Command
	err := withTokenTx(ctx, pool, rawToken, func(ctx context.Context, tx pgx.Tx, auth AuthenticatedAgent) error {
		row := tx.QueryRow(ctx, `
			UPDATE edge_agent_commands SET status = $1, acknowledged_at = now()
			WHERE id = $2 AND agent_id = $3 AND firm_id = $4
			RETURNING `+commandColumns, string(CommandStatusAcknowledged), commandID, auth.AgentID, auth.FirmID)
		c, err := scanCommand(row)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrCommandNotFound
			}
			return fmt.Errorf("acknowledge edge agent command: %w", err)
		}
		command = c
		return nil
	})
	if err != nil {
		return Command{}, err
	}
	return command, nil
}
