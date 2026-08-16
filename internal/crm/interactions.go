// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package crm

import (
	"context"
	"errors"
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

const interactionAuditEntityType = "crm_interaction"
const createInteractionAuditAction = "create"

// interactionTypes mirrors crm_interactions.type's own CHECK constraint
// (migrations/0029_crm_projects_depth.up.sql) - validated in Go too so a
// bad value comes back as a clean ErrInvalidInteraction, the same "app
// check first, DB constraint as defense in depth" pattern this codebase's
// other CHECK-backed columns already follow.
var interactionTypes = map[string]bool{
	"call": true, "email": true, "meeting": true, "note": true, "demo": true, "proposal": true,
}

// Interaction is one crm_interactions row.
type Interaction struct {
	ID             uuid.UUID
	FirmID         uuid.UUID
	CustomerID     uuid.UUID
	Type           string
	Date           time.Time
	Summary        string
	Outcome        *string
	NextAction     *string
	NextActionDate *time.Time
	OpportunityID  *uuid.UUID
	CreatedBy      uuid.UUID
	CreatedAt      time.Time
}

const interactionColumns = `id, firm_id, customer_id, type, date, summary, outcome, next_action, next_action_date, opportunity_id, created_by, created_at`

func scanInteractionRow(row pgx.Row) (Interaction, error) {
	var i Interaction
	if err := row.Scan(&i.ID, &i.FirmID, &i.CustomerID, &i.Type, &i.Date, &i.Summary, &i.Outcome,
		&i.NextAction, &i.NextActionDate, &i.OpportunityID, &i.CreatedBy, &i.CreatedAt); err != nil {
		return Interaction{}, err
	}
	return i, nil
}

// CreateInteractionInput is CreateInteraction's request shape.
type CreateInteractionInput struct {
	CustomerID     uuid.UUID
	Type           string
	Date           time.Time
	Summary        string
	Outcome        string
	NextAction     string
	NextActionDate string // optional, YYYY-MM-DD
	OpportunityID  *uuid.UUID
}

// CreateInteraction logs a new interaction against customerID - a call,
// email, meeting, note, demo, or proposal. Owner-gated, same tier as
// CreateCustomer. OpportunityID, when set, must name a real opportunity
// in this firm (checked here directly, not through
// internal/workflow.FieldTypeReference-style validation - this is a
// plain HTTP handler input, not a workflow instance payload).
func CreateInteraction(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateInteractionInput) (Interaction, error) {
	typ := strings.TrimSpace(input.Type)
	if !interactionTypes[typ] {
		return Interaction{}, fmt.Errorf("%w: type %q is not one of call/email/meeting/note/demo/proposal", ErrInvalidInteraction, typ)
	}
	summary := strings.TrimSpace(input.Summary)
	if summary == "" {
		return Interaction{}, fmt.Errorf("%w: summary must not be empty", ErrInvalidInteraction)
	}
	if input.Date.IsZero() {
		return Interaction{}, fmt.Errorf("%w: date must be set", ErrInvalidInteraction)
	}
	nextActionDate, err := parseOptionalDate(input.NextActionDate)
	if err != nil {
		return Interaction{}, fmt.Errorf("%w: nextActionDate must be YYYY-MM-DD", ErrInvalidInteraction)
	}

	var i Interaction
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

		if input.OpportunityID != nil {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM crm_opportunities WHERE id = $1 AND firm_id = $2)`, *input.OpportunityID, firmID).Scan(&exists); err != nil {
				return fmt.Errorf("look up opportunity: %w", err)
			}
			if !exists {
				return fmt.Errorf("%w: opportunityId does not name a real opportunity in this firm", ErrInvalidInteraction)
			}
		}

		row := tx.QueryRow(ctx, `
			INSERT INTO crm_interactions (firm_id, customer_id, type, date, summary, outcome, next_action, next_action_date, opportunity_id, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING `+interactionColumns,
			firmID, input.CustomerID, typ, input.Date, summary, optionalString(input.Outcome),
			optionalString(input.NextAction), nextActionDate, input.OpportunityID, userID)
		i, err = scanInteractionRow(row)
		if err != nil {
			return fmt.Errorf("insert interaction: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, i.ID, interactionAuditEntityType, createInteractionAuditAction, map[string]any{
			"customerId": i.CustomerID, "type": i.Type,
		})
	})
	if err != nil {
		return Interaction{}, err
	}
	return i, nil
}

// ListInteractions returns customerID's interactions within firmID,
// newest first. Member-gated.
func ListInteractions(ctx context.Context, pool *pgxpool.Pool, firmID, userID, customerID uuid.UUID) ([]Interaction, error) {
	var interactions []Interaction
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+interactionColumns+` FROM crm_interactions WHERE firm_id = $1 AND customer_id = $2 ORDER BY date DESC`, firmID, customerID)
		if err != nil {
			return fmt.Errorf("list interactions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			i, err := scanInteractionRow(rows)
			if err != nil {
				return err
			}
			interactions = append(interactions, i)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return interactions, nil
}

// GetInteraction resolves one interaction by id within firmID.
// Member-gated.
func GetInteraction(ctx context.Context, pool *pgxpool.Pool, firmID, userID, interactionID uuid.UUID) (Interaction, error) {
	var i Interaction
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+interactionColumns+` FROM crm_interactions WHERE id = $1 AND firm_id = $2`, interactionID, firmID)
		i, err = scanInteractionRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInteractionNotFound
		}
		return err
	})
	if err != nil {
		return Interaction{}, err
	}
	return i, nil
}
