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

const opportunityAuditEntityType = "crm_opportunity"

const (
	createOpportunityAuditAction = "create"
	updateOpportunityAuditAction = "update"
)

// OpportunityStage is crm_opportunities.stage's closed vocabulary -
// mirrors migrations/0029_crm_projects_depth.up.sql's own CHECK
// constraint. won/lost are terminal: UpdateOpportunityStage refuses to
// move an opportunity out of either one - a closed deal doesn't reopen,
// the same "terminal state" concept internal/manufacturing's
// ProductionOrderStatus/internal/warehouse's TransferStatus already use,
// just without a real workflow-engine state machine behind it (an
// opportunity's stage is a plain column, not a workflow_instances row -
// deliberately, since the CRM pipeline's own conversion already goes
// through the customer_pipeline workflow; a second, parallel workflow for
// opportunity stages would duplicate that mechanism for no benefit here).
type OpportunityStage string

const (
	OpportunityStageProspect    OpportunityStage = "prospect"
	OpportunityStageQualified   OpportunityStage = "qualified"
	OpportunityStageProposal    OpportunityStage = "proposal"
	OpportunityStageNegotiation OpportunityStage = "negotiation"
	OpportunityStageWon         OpportunityStage = "won"
	OpportunityStageLost        OpportunityStage = "lost"
)

var validOpportunityStages = map[OpportunityStage]bool{
	OpportunityStageProspect: true, OpportunityStageQualified: true, OpportunityStageProposal: true,
	OpportunityStageNegotiation: true, OpportunityStageWon: true, OpportunityStageLost: true,
}

func isTerminalStage(s OpportunityStage) bool {
	return s == OpportunityStageWon || s == OpportunityStageLost
}

// Opportunity is one crm_opportunities row.
type Opportunity struct {
	ID                 uuid.UUID
	FirmID             uuid.UUID
	CustomerID         uuid.UUID
	Name               string
	Value              *string
	Currency           string
	Stage              OpportunityStage
	Probability        *int
	ExpectedCloseDate  *time.Time
	AssignedToPersonID *uuid.UUID
	Notes              *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

const opportunityColumns = `id, firm_id, customer_id, name, value::text, currency, stage, probability, expected_close_date, assigned_to_person_id, notes, created_at, updated_at`

func scanOpportunityRow(row pgx.Row) (Opportunity, error) {
	var o Opportunity
	var stage string
	if err := row.Scan(&o.ID, &o.FirmID, &o.CustomerID, &o.Name, &o.Value, &o.Currency, &stage,
		&o.Probability, &o.ExpectedCloseDate, &o.AssignedToPersonID, &o.Notes, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return Opportunity{}, err
	}
	o.Stage = OpportunityStage(stage)
	return o, nil
}

// CreateOpportunityInput is CreateOpportunity's request shape.
type CreateOpportunityInput struct {
	CustomerID         uuid.UUID
	Name               string
	Value              string // optional decimal string
	Currency           string
	Probability        *int
	ExpectedCloseDate  string // optional, YYYY-MM-DD
	AssignedToPersonID *uuid.UUID
	Notes              string
}

// CreateOpportunity adds a new opportunity to firmID, in the default
// "prospect" stage. Owner-gated, same tier as CreateCustomer.
func CreateOpportunity(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateOpportunityInput) (Opportunity, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Opportunity{}, fmt.Errorf("%w: name must not be empty", ErrInvalidOpportunity)
	}
	value, err := decimalOrNil(input.Value)
	if err != nil {
		return Opportunity{}, fmt.Errorf("%w: value must be a positive decimal", ErrInvalidOpportunity)
	}
	if input.Probability != nil && (*input.Probability < 0 || *input.Probability > 100) {
		return Opportunity{}, fmt.Errorf("%w: probability must be between 0 and 100", ErrInvalidOpportunity)
	}
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = defaultCustomerCurrency
	}
	expectedClose, err := parseOptionalDate(input.ExpectedCloseDate)
	if err != nil {
		return Opportunity{}, fmt.Errorf("%w: expectedCloseDate must be YYYY-MM-DD", ErrInvalidOpportunity)
	}

	var o Opportunity
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

		row := tx.QueryRow(ctx, `
			INSERT INTO crm_opportunities (firm_id, customer_id, name, value, currency, probability, expected_close_date, assigned_to_person_id, notes)
			VALUES ($1, $2, $3, $4::numeric, $5, $6, $7, $8, $9)
			RETURNING `+opportunityColumns,
			firmID, input.CustomerID, name, value, currency, input.Probability, expectedClose, input.AssignedToPersonID, optionalString(input.Notes))
		o, err = scanOpportunityRow(row)
		if err != nil {
			return fmt.Errorf("insert opportunity: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, o.ID, opportunityAuditEntityType, createOpportunityAuditAction, map[string]any{
			"customerId": o.CustomerID, "name": o.Name,
		})
	})
	if err != nil {
		return Opportunity{}, err
	}
	return o, nil
}

// ListOpportunities returns firmID's opportunities, optionally filtered
// to one customer, newest first. Member-gated.
func ListOpportunities(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, customerID *uuid.UUID) ([]Opportunity, error) {
	var opportunities []Opportunity
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT `+opportunityColumns+` FROM crm_opportunities
			WHERE firm_id = $1 AND ($2::uuid IS NULL OR customer_id = $2)
			ORDER BY created_at DESC
		`, firmID, customerID)
		if err != nil {
			return fmt.Errorf("list opportunities: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			o, err := scanOpportunityRow(rows)
			if err != nil {
				return err
			}
			opportunities = append(opportunities, o)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return opportunities, nil
}

// GetOpportunity resolves one opportunity by id within firmID.
// Member-gated.
func GetOpportunity(ctx context.Context, pool *pgxpool.Pool, firmID, userID, opportunityID uuid.UUID) (Opportunity, error) {
	var o Opportunity
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+opportunityColumns+` FROM crm_opportunities WHERE id = $1 AND firm_id = $2`, opportunityID, firmID)
		o, err = scanOpportunityRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOpportunityNotFound
		}
		return err
	})
	if err != nil {
		return Opportunity{}, err
	}
	return o, nil
}

// UpdateOpportunityInput is UpdateOpportunity's partial-update input -
// nil/unset leaves that field unchanged. Stage is deliberately NOT here:
// changing stage goes through UpdateOpportunityStage, which enforces the
// terminal-stage rule this function doesn't need to know about.
type UpdateOpportunityInput struct {
	Name               *string
	Value              *string
	Probability        *int
	ExpectedCloseDate  *string
	AssignedToPersonID *uuid.UUID
	Notes              *string
}

// UpdateOpportunity applies input's changes to opportunityID. Owner-gated.
func UpdateOpportunity(ctx context.Context, pool *pgxpool.Pool, firmID, userID, opportunityID uuid.UUID, input UpdateOpportunityInput) (Opportunity, error) {
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		return Opportunity{}, fmt.Errorf("%w: name must not be empty", ErrInvalidOpportunity)
	}
	var value *string
	var valueSet bool
	if input.Value != nil {
		valueSet = true
		v, err := decimalOrNil(*input.Value)
		if err != nil {
			return Opportunity{}, fmt.Errorf("%w: value must be a positive decimal", ErrInvalidOpportunity)
		}
		value = v
	}
	if input.Probability != nil && (*input.Probability < 0 || *input.Probability > 100) {
		return Opportunity{}, fmt.Errorf("%w: probability must be between 0 and 100", ErrInvalidOpportunity)
	}
	var expectedClose *time.Time
	var expectedCloseSet bool
	if input.ExpectedCloseDate != nil {
		expectedCloseSet = true
		d, err := parseOptionalDate(*input.ExpectedCloseDate)
		if err != nil {
			return Opportunity{}, fmt.Errorf("%w: expectedCloseDate must be YYYY-MM-DD", ErrInvalidOpportunity)
		}
		expectedClose = d
	}

	var o Opportunity
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

		row := tx.QueryRow(ctx, `
			UPDATE crm_opportunities SET
				name = COALESCE($1, name),
				value = CASE WHEN $2::boolean THEN $3::numeric ELSE value END,
				probability = COALESCE($4, probability),
				expected_close_date = CASE WHEN $5::boolean THEN $6::date ELSE expected_close_date END,
				assigned_to_person_id = COALESCE($7, assigned_to_person_id),
				notes = CASE WHEN $8::boolean THEN $9 ELSE notes END,
				updated_at = now()
			WHERE id = $10 AND firm_id = $11
			RETURNING `+opportunityColumns,
			input.Name, valueSet, value, input.Probability, expectedCloseSet, expectedClose,
			input.AssignedToPersonID, input.Notes != nil, optionalString(derefOr(input.Notes, "")),
			opportunityID, firmID)
		o, err = scanOpportunityRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOpportunityNotFound
		}
		if err != nil {
			return fmt.Errorf("update opportunity: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, o.ID, opportunityAuditEntityType, updateOpportunityAuditAction, map[string]any{"name": o.Name})
	})
	if err != nil {
		return Opportunity{}, err
	}
	return o, nil
}

// UpdateOpportunityStage moves opportunityID to newStage. Refuses if the
// opportunity is already in a terminal stage (won/lost - see
// OpportunityStage's own doc comment) or if newStage isn't one of the
// six valid stages. Owner-gated.
func UpdateOpportunityStage(ctx context.Context, pool *pgxpool.Pool, firmID, userID, opportunityID uuid.UUID, newStage OpportunityStage) (Opportunity, error) {
	if !validOpportunityStages[newStage] {
		return Opportunity{}, fmt.Errorf("%w: stage %q is not one of prospect/qualified/proposal/negotiation/won/lost", ErrInvalidOpportunity, newStage)
	}

	var o Opportunity
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

		var currentStage string
		if err := tx.QueryRow(ctx, `SELECT stage FROM crm_opportunities WHERE id = $1 AND firm_id = $2 FOR UPDATE`, opportunityID, firmID).Scan(&currentStage); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrOpportunityNotFound
			}
			return fmt.Errorf("look up opportunity: %w", err)
		}
		if isTerminalStage(OpportunityStage(currentStage)) {
			return fmt.Errorf("%w: opportunity is already %q, a closed deal doesn't reopen", ErrInvalidOpportunityTransition, currentStage)
		}

		row := tx.QueryRow(ctx, `
			UPDATE crm_opportunities SET stage = $1, updated_at = now() WHERE id = $2 AND firm_id = $3
			RETURNING `+opportunityColumns, string(newStage), opportunityID, firmID)
		o, err = scanOpportunityRow(row)
		if err != nil {
			return fmt.Errorf("update opportunity stage: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, o.ID, opportunityAuditEntityType, updateOpportunityAuditAction, map[string]any{"stage": string(newStage)})
	})
	if err != nil {
		return Opportunity{}, err
	}
	return o, nil
}

func parseOptionalDate(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
