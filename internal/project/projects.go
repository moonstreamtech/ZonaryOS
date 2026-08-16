// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package project is the project management foundation Vision names
// ("görev ve onay akışları" - task and approval flows) beyond the
// task_approval workflow's own minimal generic task: a real project
// record that task_approval instances can optionally link to (via
// internal/workflow's FieldTypeProject, an optional project_id payload
// field), with a summary view of a project's linked tasks and logged
// time. Not a full PM suite: no Gantt charts.
//
// Authorization follows this codebase's usual discipline: IsMember
// before any read, IsOwner before any write - the same tier
// internal/crm's customer/opportunity mutations use.
package project

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/auditlog"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

const projectAuditEntityType = "project"

const (
	createProjectAuditAction = "create"
	updateProjectAuditAction = "update"
)

// amountPattern mirrors internal/accounting's/internal/crm's own
// amountPattern - a plain positive decimal string, matching
// projects.budget's numeric(19,4) column. Redeclared here rather than
// imported cross-package, same reasoning every other package's own copy
// gives.
var amountPattern = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,4})?$`)

func decimalOrNil(s string) (*string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if !amountPattern.MatchString(s) {
		return nil, fmt.Errorf("%w: %q must be a positive decimal with at most 4 fraction digits", ErrInvalidProject, s)
	}
	return &s, nil
}

func optionalString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
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

const defaultProjectCurrency = "TRY"

// ProjectStatus is projects.status's closed vocabulary - mirrors
// migrations/0029_crm_projects_depth.up.sql's own CHECK constraint.
type ProjectStatus string

const (
	ProjectStatusActive    ProjectStatus = "active"
	ProjectStatusOnHold    ProjectStatus = "on_hold"
	ProjectStatusCompleted ProjectStatus = "completed"
	ProjectStatusCancelled ProjectStatus = "cancelled"
)

var validProjectStatuses = map[ProjectStatus]bool{
	ProjectStatusActive: true, ProjectStatusOnHold: true, ProjectStatusCompleted: true, ProjectStatusCancelled: true,
}

// Project is one projects row.
type Project struct {
	ID              uuid.UUID
	FirmID          uuid.UUID
	Name            string
	Description     *string
	Status          ProjectStatus
	StartDate       *time.Time
	EndDate         *time.Time
	Budget          *string
	Currency        string
	ManagerPersonID *uuid.UUID
	CreatedAt       time.Time
}

const projectColumns = `id, firm_id, name, description, status, start_date, end_date, budget::text, currency, manager_person_id, created_at`

func scanProjectRow(row pgx.Row) (Project, error) {
	var p Project
	var status string
	if err := row.Scan(&p.ID, &p.FirmID, &p.Name, &p.Description, &status, &p.StartDate, &p.EndDate,
		&p.Budget, &p.Currency, &p.ManagerPersonID, &p.CreatedAt); err != nil {
		return Project{}, err
	}
	p.Status = ProjectStatus(status)
	return p, nil
}

// CreateProjectInput is CreateProject's request shape.
type CreateProjectInput struct {
	Name            string
	Description     string
	StartDate       string // optional, YYYY-MM-DD
	EndDate         string // optional, YYYY-MM-DD
	Budget          string // optional decimal string
	Currency        string
	ManagerPersonID *uuid.UUID
}

// CreateProject adds a new project to firmID, in the default "active"
// status. Owner-gated.
func CreateProject(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input CreateProjectInput) (Project, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Project{}, fmt.Errorf("%w: name must not be empty", ErrInvalidProject)
	}
	budget, err := decimalOrNil(input.Budget)
	if err != nil {
		return Project{}, err
	}
	startDate, err := parseOptionalDate(input.StartDate)
	if err != nil {
		return Project{}, fmt.Errorf("%w: startDate must be YYYY-MM-DD", ErrInvalidProject)
	}
	endDate, err := parseOptionalDate(input.EndDate)
	if err != nil {
		return Project{}, fmt.Errorf("%w: endDate must be YYYY-MM-DD", ErrInvalidProject)
	}
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = defaultProjectCurrency
	}

	var p Project
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
			INSERT INTO projects (firm_id, name, description, start_date, end_date, budget, currency, manager_person_id)
			VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8)
			RETURNING `+projectColumns,
			firmID, name, optionalString(input.Description), startDate, endDate, budget, currency, input.ManagerPersonID)
		p, err = scanProjectRow(row)
		if err != nil {
			return fmt.Errorf("insert project: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, p.ID, projectAuditEntityType, createProjectAuditAction, map[string]any{"name": p.Name})
	})
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

// ListProjectsOptions filters ListProjects - a zero value returns every
// project, matching this codebase's usual convention.
type ListProjectsOptions struct {
	Status ProjectStatus
}

// ListProjects returns firmID's projects, newest first. Member-gated.
func ListProjects(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, opts ListProjectsOptions) ([]Project, error) {
	var projects []Project
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `
			SELECT `+projectColumns+` FROM projects
			WHERE firm_id = $1 AND ($2 = '' OR status = $2)
			ORDER BY created_at DESC
		`, firmID, string(opts.Status))
		if err != nil {
			return fmt.Errorf("list projects: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanProjectRow(rows)
			if err != nil {
				return err
			}
			projects = append(projects, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return projects, nil
}

// GetProject resolves one project by id within firmID. Member-gated.
func GetProject(ctx context.Context, pool *pgxpool.Pool, firmID, userID, projectID uuid.UUID) (Project, error) {
	var p Project
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = $1 AND firm_id = $2`, projectID, firmID)
		p, err = scanProjectRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProjectNotFound
		}
		return err
	})
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

// UpdateProjectInput is UpdateProject's partial-update input - nil/unset
// leaves that field unchanged, the same convention
// internal/crm.UpdateOpportunityInput uses.
type UpdateProjectInput struct {
	Name            *string
	Description     *string
	Status          *ProjectStatus
	StartDate       *string
	EndDate         *string
	Budget          *string
	ManagerPersonID *uuid.UUID
}

// UpdateProject applies input's changes to projectID. Owner-gated.
func UpdateProject(ctx context.Context, pool *pgxpool.Pool, firmID, userID, projectID uuid.UUID, input UpdateProjectInput) (Project, error) {
	if input.Name != nil && strings.TrimSpace(*input.Name) == "" {
		return Project{}, fmt.Errorf("%w: name must not be empty", ErrInvalidProject)
	}
	if input.Status != nil && !validProjectStatuses[*input.Status] {
		return Project{}, fmt.Errorf("%w: status %q is not one of active/on_hold/completed/cancelled", ErrInvalidProject, *input.Status)
	}
	var budget *string
	var budgetSet bool
	if input.Budget != nil {
		budgetSet = true
		b, err := decimalOrNil(*input.Budget)
		if err != nil {
			return Project{}, err
		}
		budget = b
	}
	var startDate, endDate *time.Time
	var startDateSet, endDateSet bool
	if input.StartDate != nil {
		startDateSet = true
		d, err := parseOptionalDate(*input.StartDate)
		if err != nil {
			return Project{}, fmt.Errorf("%w: startDate must be YYYY-MM-DD", ErrInvalidProject)
		}
		startDate = d
	}
	if input.EndDate != nil {
		endDateSet = true
		d, err := parseOptionalDate(*input.EndDate)
		if err != nil {
			return Project{}, fmt.Errorf("%w: endDate must be YYYY-MM-DD", ErrInvalidProject)
		}
		endDate = d
	}
	var status *string
	if input.Status != nil {
		s := string(*input.Status)
		status = &s
	}

	var p Project
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
			UPDATE projects SET
				name = COALESCE($1, name),
				description = CASE WHEN $2::boolean THEN $3 ELSE description END,
				status = COALESCE($4, status),
				start_date = CASE WHEN $5::boolean THEN $6::date ELSE start_date END,
				end_date = CASE WHEN $7::boolean THEN $8::date ELSE end_date END,
				budget = CASE WHEN $9::boolean THEN $10::numeric ELSE budget END,
				manager_person_id = COALESCE($11, manager_person_id)
			WHERE id = $12 AND firm_id = $13
			RETURNING `+projectColumns,
			input.Name, input.Description != nil, optionalString(derefOr(input.Description, "")), status,
			startDateSet, startDate, endDateSet, endDate, budgetSet, budget, input.ManagerPersonID, projectID, firmID)
		p, err = scanProjectRow(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProjectNotFound
		}
		if err != nil {
			return fmt.Errorf("update project: %w", err)
		}

		return auditlog.Write(ctx, tx, firmID, userID, p.ID, projectAuditEntityType, updateProjectAuditAction, map[string]any{"name": p.Name})
	})
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

// ProjectExistsTx reports whether projectID is a real projects row
// within firmID - the cross-module check internal/workflow's
// FieldTypeProject payload validation uses, the same shape
// crm.CustomerExistsTx/inventory.ProductExistsTx already establish.
//
// ciaudit:ignore-firmid-check: internal cross-package helper, called only
// by internal/workflow.projectValidator, itself only reachable from
// CreateInstance after permission.IsMember/Has have already run in the
// same transaction - firmID here scopes the query (defense in depth
// alongside RLS), it is not itself an authorization decision.
func ProjectExistsTx(ctx context.Context, tx pgx.Tx, firmID, projectID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1 AND firm_id = $2)
	`, projectID, firmID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check project exists: %w", err)
	}
	return exists, nil
}
