// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/onboarding"
	"github.com/moonstreamtech/ZonaryOS/internal/permission"
	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
)

// ErrInvalidQuerySpec means a QuerySpec referenced an entity, field, or
// aggregation not in entityRegistry, or was otherwise structurally
// invalid (e.g. no metrics) - always maps to HTTP 400, never 500: a
// malformed report_definitions.query_spec is a caller error, not a server
// fault, and must never be silently downgraded to "no results" (which
// would hide the same information-disclosure risk BuildQuery's own doc
// comment describes).
var ErrInvalidQuerySpec = errors.New("invalid report query spec")

// ErrReportDefinitionNotFound means no report_definitions row with the
// given id is visible in the caller's firm context.
var ErrReportDefinitionNotFound = errors.New("report definition not found")

// ReportDefinition is one report_definitions row.
type ReportDefinition struct {
	ID          uuid.UUID
	FirmID      uuid.UUID
	Name        string
	Description *string
	QuerySpec   QuerySpec
	CreatedBy   uuid.UUID
	CreatedAt   time.Time
}

const reportDefinitionColumns = "id, firm_id, name, description, query_spec, created_by, created_at"

func scanReportDefinition(row pgx.Row) (ReportDefinition, error) {
	var d ReportDefinition
	var rawSpec []byte
	if err := row.Scan(&d.ID, &d.FirmID, &d.Name, &d.Description, &rawSpec, &d.CreatedBy, &d.CreatedAt); err != nil {
		return ReportDefinition{}, err
	}
	if err := json.Unmarshal(rawSpec, &d.QuerySpec); err != nil {
		return ReportDefinition{}, fmt.Errorf("decode query_spec: %w", err)
	}
	return d, nil
}

// ReportDefinitionInput is CreateReportDefinition's own request shape.
type ReportDefinitionInput struct {
	Name        string
	Description string
	QuerySpec   QuerySpec
}

// validateQuerySpec rejects a structurally invalid spec (unknown entity/
// field/aggregation) at definition-save time, not just at run time - the
// same "fail fast, at the boundary" reasoning every other package's own
// input validation follows. Checks entityRegistry (via BuildQuery, run
// against a placeholder firm id purely for its own validation side
// effect - it makes no database call) first, then falls back to a
// registered ExternalSource (plugin/extension architecture batch, Part
// 2) - the same lookup order executeQuerySpec's own doc comment
// describes for the run-time path, so a spec that validates here is
// guaranteed to also run successfully later.
func validateQuerySpec(spec QuerySpec) error {
	if _, builtIn := entityRegistry[spec.Entity]; builtIn {
		_, err := BuildQuery(uuid.Nil, spec)
		return err
	}
	if source, ok := externalSources[spec.Entity]; ok {
		return validateExternalQuerySpec(spec, source)
	}
	return fmt.Errorf("%w: unknown entity %q", ErrInvalidQuerySpec, spec.Entity)
}

// CreateReportDefinition saves a new report_definitions row - member-
// gated (not owner-gated): defining a report over data the caller can
// already see is the same tier as internal/workflow.DefineWorkflowForFirm's
// counterpart reads, not a structural firm decision.
func CreateReportDefinition(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID, input ReportDefinitionInput) (ReportDefinition, error) {
	if input.Name == "" {
		return ReportDefinition{}, fmt.Errorf("%w: name must not be empty", ErrInvalidQuerySpec)
	}
	if err := validateQuerySpec(input.QuerySpec); err != nil {
		return ReportDefinition{}, err
	}
	specJSON, err := json.Marshal(input.QuerySpec)
	if err != nil {
		return ReportDefinition{}, fmt.Errorf("encode query_spec: %w", err)
	}

	var def ReportDefinition
	err = zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		var description *string
		if input.Description != "" {
			description = &input.Description
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO report_definitions (firm_id, name, description, query_spec, created_by)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING `+reportDefinitionColumns, firmID, input.Name, description, specJSON, userID)
		d, err := scanReportDefinition(row)
		if err != nil {
			return fmt.Errorf("insert report definition: %w", err)
		}
		def = d
		return nil
	})
	if err != nil {
		return ReportDefinition{}, err
	}
	return def, nil
}

// ListReportDefinitions returns every report_definitions row for firmID -
// member-gated read, the "My Reports" list.
func ListReportDefinitions(ctx context.Context, pool *pgxpool.Pool, firmID, userID uuid.UUID) ([]ReportDefinition, error) {
	var defs []ReportDefinition
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		rows, err := tx.Query(ctx, `SELECT `+reportDefinitionColumns+` FROM report_definitions WHERE firm_id = $1 ORDER BY created_at DESC`, firmID)
		if err != nil {
			return fmt.Errorf("list report definitions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scanReportDefinition(rows)
			if err != nil {
				return fmt.Errorf("scan report definition: %w", err)
			}
			defs = append(defs, d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return defs, nil
}

// GetReportDefinition returns one report_definitions row - member-gated.
func GetReportDefinition(ctx context.Context, pool *pgxpool.Pool, firmID, userID, definitionID uuid.UUID) (ReportDefinition, error) {
	var def ReportDefinition
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		row := tx.QueryRow(ctx, `SELECT `+reportDefinitionColumns+` FROM report_definitions WHERE id = $1 AND firm_id = $2`, definitionID, firmID)
		d, err := scanReportDefinition(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrReportDefinitionNotFound
			}
			return fmt.Errorf("get report definition: %w", err)
		}
		def = d
		return nil
	})
	if err != nil {
		return ReportDefinition{}, err
	}
	return def, nil
}

// ReportRow is one result row from RunReport - GroupKey is nil when the
// definition's query_spec has no group_by. Metrics is keyed by each
// QueryMetric's own Name, in the same order QuerySpec.Metrics listed them.
type ReportRow struct {
	GroupKey *string        `json:"groupKey,omitempty"`
	Metrics  map[string]any `json:"metrics"`
}

// RunReport executes definitionID's query_spec synchronously against
// firmID's data via BuildQuery's parameterized SQL, records the run (and
// its result or error) in saved_report_runs, and returns the rows.
// Member-gated: running a report over data the caller can already see is
// ordinary read access, the same tier as GetDashboardKPIs.
func RunReport(ctx context.Context, pool *pgxpool.Pool, firmID, userID, definitionID uuid.UUID) ([]ReportRow, error) {
	var rows []ReportRow
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		isMember, err := permission.IsMember(ctx, tx, firmID, userID)
		if err != nil {
			return err
		}
		if !isMember {
			return ErrFirmNotFound
		}

		defRow := tx.QueryRow(ctx, `SELECT `+reportDefinitionColumns+` FROM report_definitions WHERE id = $1 AND firm_id = $2`, definitionID, firmID)
		def, err := scanReportDefinition(defRow)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrReportDefinitionNotFound
			}
			return fmt.Errorf("get report definition: %w", err)
		}

		var runID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO saved_report_runs (firm_id, definition_id, run_by)
			VALUES ($1, $2, $3) RETURNING id
		`, firmID, definitionID, userID).Scan(&runID); err != nil {
			return fmt.Errorf("insert report run: %w", err)
		}

		result, runErr := executeQuerySpec(ctx, tx, pool, firmID, def.QuerySpec)
		if runErr != nil {
			errText := runErr.Error()
			if _, err := tx.Exec(ctx, `
				UPDATE saved_report_runs SET status = 'failed', error_text = $1, finished_at = now() WHERE id = $2
			`, errText, runID); err != nil {
				return fmt.Errorf("record failed report run: %w", err)
			}
			return runErr
		}

		resultJSON, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("encode report result: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE saved_report_runs SET status = 'completed', result = $1, finished_at = now() WHERE id = $2
		`, resultJSON, runID); err != nil {
			return fmt.Errorf("record completed report run: %w", err)
		}

		rows = result
		return nil
	})
	if err != nil {
		return nil, err
	}
	// onboarding.CompleteStep (see that package's own doc comment): fire-
	// and-forget, never allowed to fail this request - the
	// run_first_report onboarding checklist item.
	onboarding.CompleteStep(pool, firmID, userID, onboarding.StepRunFirstReport)
	return rows, nil
}

// executeQuerySpec runs spec's BuildQuery-translated SQL and scans the
// dynamic (group_key + N metric) columns back into ReportRow values -
// UNLESS spec.Entity is a plugin-provided one (plugin/extension
// architecture batch, Part 2), in which case it delegates entirely to
// that ExternalSource's own Query instead of ever reaching BuildQuery.
// entityRegistry is checked FIRST (see BuildQuery's own lookup) so a
// plugin can never shadow a built-in entity name - the built-in always
// wins a collision, the only place this ambiguity is resolved.
//
// ciaudit:ignore-firmid-check: internal helper called only by RunReport,
// which already runs permission.IsMember before reaching this call -
// firmID here scopes the query (defense in depth alongside RLS), it is
// not itself an authorization decision.
func executeQuerySpec(ctx context.Context, tx pgx.Tx, pool *pgxpool.Pool, firmID uuid.UUID, spec QuerySpec) ([]ReportRow, error) {
	if _, builtIn := entityRegistry[spec.Entity]; !builtIn {
		if source, ok := externalSources[spec.Entity]; ok {
			return executeExternalQuerySpec(ctx, pool, firmID, spec, source)
		}
	}

	q, err := BuildQuery(firmID, spec)
	if err != nil {
		return nil, err
	}

	dbRows, err := tx.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, fmt.Errorf("execute report query: %w", err)
	}
	defer dbRows.Close()

	var results []ReportRow
	for dbRows.Next() {
		scanTargets := make([]any, 0, len(q.metricNames)+1)
		var groupKey *string
		if q.hasGroupBy {
			scanTargets = append(scanTargets, &groupKey)
		}
		metricValues := make([]any, len(q.metricNames))
		for i := range metricValues {
			scanTargets = append(scanTargets, &metricValues[i])
		}
		if err := dbRows.Scan(scanTargets...); err != nil {
			return nil, fmt.Errorf("scan report row: %w", err)
		}
		metrics := make(map[string]any, len(q.metricNames))
		for i, name := range q.metricNames {
			metrics[name] = metricValues[i]
		}
		results = append(results, ReportRow{GroupKey: groupKey, Metrics: metrics})
	}
	if err := dbRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate report rows: %w", err)
	}
	return results, nil
}
