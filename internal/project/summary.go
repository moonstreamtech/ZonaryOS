// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package project

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// taskApprovalDefinitionKey mirrors internal/workflow.TaskApprovalKey's
// own literal value - redeclared here rather than imported cross-package
// (internal/workflow already imports internal/project for
// ProjectExistsTx/the FieldTypeProject validator; importing back would be
// a cycle) - a plain string constant, the same "reference a well-known
// key by value, not by importing the defining package" judgment call
// this codebase already makes for reason/status text constants.
const taskApprovalDefinitionKey = "task_approval"

// TaskStateCount is one entry in Summary.TasksByState.
type TaskStateCount struct {
	StateKey string
	Count    int
}

// Summary is GetProjectSummary's result: how many linked task_approval
// instances sit in each state, how many hours have been logged against
// them, and a budget-vs-estimated-cost comparison.
type Summary struct {
	ProjectID        uuid.UUID
	TasksByState     []TaskStateCount
	TotalTasks       int
	TotalHoursLogged string
	// Budget is the project's own budget (nil if unset).
	Budget *string
	// EstimatedCost is TotalHoursLogged * the firm's average hourly
	// contract rate (nil if the firm has no "hourly" contracts to derive
	// an average from - "if available", per this batch's own design
	// brief).
	EstimatedCost *string
}

// GetProjectSummary computes projectID's task/time/budget summary.
// Member-gated.
//
// Tasks are linked via workflow_instances.payload->>'project_id' (the
// FieldTypeProject payload field internal/workflow.TaskApprovalSpec
// optionally carries) - not a real foreign key column on
// workflow_instances (payload stays the freeform jsonb bag it already is
// for every other cross-module reference field, e.g. assignee_person_id;
// project_id is no different). Hours are summed from
// time_entries.workflow_instance_id (already a real FK, from the
// payroll/time-tracking batch) restricted to that same set of task
// instance ids.
func GetProjectSummary(ctx context.Context, pool *pgxpool.Pool, firmID, userID, projectID uuid.UUID) (Summary, error) {
	var s Summary
	s.ProjectID = projectID
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1 AND firm_id = $2)`, projectID, firmID).Scan(&exists); err != nil {
			return fmt.Errorf("look up project: %w", err)
		}
		if !exists {
			return ErrProjectNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT ws.key, count(*)
			FROM workflow_instances wi
			JOIN workflow_definitions wd ON wd.id = wi.workflow_definition_id
			JOIN workflow_states ws ON ws.id = wi.current_state_id
			WHERE wi.firm_id = $1 AND wd.key = $2 AND wi.payload->>'project_id' = $3
			GROUP BY ws.key
		`, firmID, taskApprovalDefinitionKey, projectID.String())
		if err != nil {
			return fmt.Errorf("count tasks by state: %w", err)
		}
		for rows.Next() {
			var c TaskStateCount
			if err := rows.Scan(&c.StateKey, &c.Count); err != nil {
				rows.Close()
				return err
			}
			s.TasksByState = append(s.TasksByState, c)
			s.TotalTasks += c.Count
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(te.hours), 0)::text
			FROM time_entries te
			JOIN workflow_instances wi ON wi.id = te.workflow_instance_id
			JOIN workflow_definitions wd ON wd.id = wi.workflow_definition_id
			WHERE wi.firm_id = $1 AND wd.key = $2 AND wi.payload->>'project_id' = $3
		`, firmID, taskApprovalDefinitionKey, projectID.String()).Scan(&s.TotalHoursLogged); err != nil {
			return fmt.Errorf("sum logged hours: %w", err)
		}

		var budget *string
		if err := tx.QueryRow(ctx, `SELECT budget::text FROM projects WHERE id = $1 AND firm_id = $2`, projectID, firmID).Scan(&budget); err != nil {
			return fmt.Errorf("look up budget: %w", err)
		}
		s.Budget = budget

		var avgHourlyRate *string
		var hasHourlyContracts bool
		if err := tx.QueryRow(ctx, `
			SELECT AVG(amount)::text, COUNT(*) > 0 FROM contracts WHERE firm_id = $1 AND type = 'hourly' AND amount IS NOT NULL
		`, firmID).Scan(&avgHourlyRate, &hasHourlyContracts); err != nil {
			return fmt.Errorf("average hourly rate: %w", err)
		}
		if hasHourlyContracts {
			var estimatedCost string
			if err := tx.QueryRow(ctx, `SELECT ($1::numeric * $2::numeric)::numeric(19,4)::text`, s.TotalHoursLogged, *avgHourlyRate).Scan(&estimatedCost); err != nil {
				return fmt.Errorf("compute estimated cost: %w", err)
			}
			s.EstimatedCost = &estimatedCost
		}

		return nil
	})
	if err != nil {
		return Summary{}, err
	}
	return s, nil
}
