// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package edgeagent is the server-side half of Vision §9's Edge Agent
// protocol: a firm registers an Edge Agent (a Go process the firm runs on
// its own hardware, under a firm-specific account), issues it a bearer
// token, and the agent then polls this server to report its status
// (heartbeat) and push events. This package is the protocol only - no
// Edge Agent binary lives in this repository (that is a separate
// deployable), no WebSocket/long-poll server-initiated push (the agent
// polls; push is a future concern), no NATS JetStream offline-resilience
// layer (Open Points item 29 - a separate design), no OTA update
// mechanism.
//
// Two entirely separate authentication paths exist side by side:
//
//   - The Keycloak path (register/list agents, issue/list tokens, list
//     events) - an ordinary firm member or owner acting through the
//     browser, verified by internal/identity.Middleware exactly like
//     every other module in this codebase.
//   - The token path (heartbeat, push an event) - the agent itself,
//     which has no Keycloak account. It authenticates with the bearer
//     token IssueToken handed it once at issuance, validated against
//     edge_agent_tokens.token_hash by TokenMiddleware - see that
//     function's own doc comment for the constant-time-compare and
//     firm_id-binding design.
//
// Authorization on the Keycloak path follows this codebase's usual
// discipline: IsMember before any read, IsOwner before any direct write
// (registering an agent or issuing a token is a structural firm decision,
// the same tier as internal/logistics.CreateDelivery).
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

const agentAuditEntityType = "edge_agent"

const (
	createAgentAuditAction = "create"
)

// AgentStatus is edge_agents.status's closed vocabulary - a real DB CHECK
// constraint (migrations/0018_edge_agent_protocol.up.sql).
type AgentStatus string

const (
	AgentStatusOffline AgentStatus = "offline"
	AgentStatusOnline  AgentStatus = "online"
	AgentStatusError   AgentStatus = "error"
)

// Agent is one edge_agents row.
type Agent struct {
	ID           uuid.UUID
	FirmID       uuid.UUID
	Name         string
	Description  *string
	Status       AgentStatus
	LastSeenAt   *time.Time
	Version      *string
	Capabilities []string
	Config       map[string]any
	CreatedAt    time.Time
}

func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// CreateAgentInput is CreateAgent's request shape.
type CreateAgentInput struct {
	Name        string
	Description string
}

// CreateAgent registers a new edge agent for firmID, in AgentStatusOffline.
// Owner-gated: registering an agent is a structural firm decision, the
// same tier as internal/logistics.CreateDelivery.
func CreateAgent(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateAgentInput) (Agent, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Agent{}, fmt.Errorf("%w: name must not be empty", ErrInvalidAgent)
	}

	var agent Agent
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
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

		agent = Agent{
			FirmID: firmID, Name: name, Description: optionalString(input.Description),
			Status: AgentStatusOffline, Capabilities: []string{}, Config: map[string]any{},
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO edge_agents (firm_id, name, description, status)
			VALUES ($1, $2, $3, $4)
			RETURNING id, created_at
		`, firmID, name, agent.Description, string(AgentStatusOffline),
		).Scan(&agent.ID, &agent.CreatedAt); err != nil {
			return fmt.Errorf("insert edge agent: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, agent.ID, agentAuditEntityType, createAgentAuditAction, map[string]any{
			"name": name,
		})
	})
	if err != nil {
		return Agent{}, err
	}
	return agent, nil
}

func scanAgent(row pgx.Row) (Agent, error) {
	var a Agent
	var statusRaw string
	var capabilitiesRaw, configRaw []byte
	if err := row.Scan(&a.ID, &a.FirmID, &a.Name, &a.Description, &statusRaw, &a.LastSeenAt, &a.Version,
		&capabilitiesRaw, &configRaw, &a.CreatedAt); err != nil {
		return Agent{}, err
	}
	a.Status = AgentStatus(statusRaw)
	if err := json.Unmarshal(capabilitiesRaw, &a.Capabilities); err != nil {
		return Agent{}, fmt.Errorf("unmarshal capabilities: %w", err)
	}
	if err := json.Unmarshal(configRaw, &a.Config); err != nil {
		return Agent{}, fmt.Errorf("unmarshal config: %w", err)
	}
	return a, nil
}

const agentColumns = "id, firm_id, name, description, status, last_seen_at, version, capabilities, config, created_at"

// ListAgents lists every edge agent registered for firmID, most recently
// created first. Member-gated read, same tier as
// internal/logistics.ListDeliveries.
func ListAgents(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]Agent, error) {
	var agents []Agent
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+agentColumns+` FROM edge_agents WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list edge agents: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			a, err := scanAgent(rows)
			if err != nil {
				return fmt.Errorf("scan edge agent: %w", err)
			}
			agents = append(agents, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return agents, nil
}

// GetAgent fetches one edge agent by id within firmID. Member-gated read.
func GetAgent(ctx context.Context, pool *pgxpool.Pool, firmID, userID, agentID uuid.UUID) (Agent, error) {
	var agent Agent
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+agentColumns+` FROM edge_agents WHERE id = $1 AND firm_id = $2`, agentID, firmID)
		a, err := scanAgent(row)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrAgentNotFound
			}
			return fmt.Errorf("get edge agent: %w", err)
		}
		agent = a
		return nil
	})
	if err != nil {
		return Agent{}, err
	}
	return agent, nil
}
