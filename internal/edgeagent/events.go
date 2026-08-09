// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package edgeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

func validAgentStatus(s AgentStatus) bool {
	switch s {
	case AgentStatusOffline, AgentStatusOnline, AgentStatusError:
		return true
	default:
		return false
	}
}

// HeartbeatInput is Heartbeat's request shape. Status defaults to
// AgentStatusOnline when empty - a heartbeat's whole point is "I am
// alive", so an agent reporting in without an explicit status is
// reporting itself online.
type HeartbeatInput struct {
	Status  AgentStatus
	Version string
}

// Heartbeat records that the agent identified by rawToken is alive:
// last_seen_at = now(), status/version updated. The agent identity comes
// entirely from the token (see withTokenTx) - there is no client-supplied
// agent or firm identifier anywhere in this call, so there is nothing for
// a caller to spoof by guessing an ID (see this package's doc comment).
func Heartbeat(ctx context.Context, pool *pgxpool.Pool, rawToken string, input HeartbeatInput) (Agent, error) {
	status := input.Status
	if status == "" {
		status = AgentStatusOnline
	}
	if !validAgentStatus(status) {
		return Agent{}, fmt.Errorf("%w: status %q is not a recognized agent status", ErrInvalidAgent, status)
	}

	var agent Agent
	err := withTokenTx(ctx, pool, rawToken, func(ctx context.Context, tx pgx.Tx, auth AuthenticatedAgent) error {
		var version *string
		if input.Version != "" {
			version = &input.Version
		}

		row := tx.QueryRow(ctx, `
			UPDATE edge_agents SET
				status = $1,
				last_seen_at = now(),
				version = COALESCE($2, version)
			WHERE id = $3 AND firm_id = $4
			RETURNING `+agentColumns+`
		`, string(status), version, auth.AgentID, auth.FirmID)
		a, err := scanAgent(row)
		if err != nil {
			if err == pgx.ErrNoRows {
				return ErrAgentNotFound
			}
			return fmt.Errorf("update edge agent heartbeat: %w", err)
		}
		agent = a
		return nil
	})
	if err != nil {
		return Agent{}, err
	}
	return agent, nil
}

// Event is one edge_events row.
type Event struct {
	ID         uuid.UUID
	FirmID     uuid.UUID
	AgentID    uuid.UUID
	EventType  string
	Payload    map[string]any
	ReceivedAt time.Time
}

// RecordEvent pushes one event on behalf of the agent identified by
// rawToken - same "identity comes only from the token" guarantee as
// Heartbeat.
func RecordEvent(ctx context.Context, pool *pgxpool.Pool, rawToken, eventType string, payload map[string]any) (Event, error) {
	if eventType == "" {
		return Event{}, fmt.Errorf("%w: eventType must not be empty", ErrInvalidAgent)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("marshal event payload: %w", err)
	}

	var event Event
	txErr := withTokenTx(ctx, pool, rawToken, func(ctx context.Context, tx pgx.Tx, auth AuthenticatedAgent) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM edge_agents WHERE id = $1 AND firm_id = $2)`, auth.AgentID, auth.FirmID).Scan(&exists); err != nil {
			return fmt.Errorf("check edge agent: %w", err)
		}
		if !exists {
			return ErrAgentNotFound
		}

		event = Event{FirmID: auth.FirmID, AgentID: auth.AgentID, EventType: eventType, Payload: payload}
		if err := tx.QueryRow(ctx, `
			INSERT INTO edge_events (firm_id, agent_id, event_type, payload)
			VALUES ($1, $2, $3, $4)
			RETURNING id, received_at
		`, auth.FirmID, auth.AgentID, eventType, payloadJSON).Scan(&event.ID, &event.ReceivedAt); err != nil {
			return fmt.Errorf("insert edge event: %w", err)
		}
		return nil
	})
	if txErr != nil {
		return Event{}, txErr
	}
	return event, nil
}

// ListEvents lists agentID's events within firmID, most recent first,
// capped at 200 rows. Member-gated read, same tier as ListAgents.
func ListEvents(ctx context.Context, pool *pgxpool.Pool, firmID, userID, agentID uuid.UUID) ([]Event, error) {
	var events []Event
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
			SELECT id, firm_id, agent_id, event_type, payload, received_at
			FROM edge_events WHERE agent_id = $1 AND firm_id = $2
			ORDER BY received_at DESC LIMIT 200
		`, agentID, firmID)
		if err != nil {
			return fmt.Errorf("list edge events: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var e Event
			var payloadRaw []byte
			if err := rows.Scan(&e.ID, &e.FirmID, &e.AgentID, &e.EventType, &payloadRaw, &e.ReceivedAt); err != nil {
				return fmt.Errorf("scan edge event: %w", err)
			}
			if err := json.Unmarshal(payloadRaw, &e.Payload); err != nil {
				return fmt.Errorf("unmarshal event payload: %w", err)
			}
			events = append(events, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}
