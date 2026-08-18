// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package activitylog is the plugin/extension architecture batch's own
// Part 3: a real, working example plugin, proving internal/plugin's hook
// system end to end WITHOUT modifying any core package (internal/workflow,
// internal/reports) beyond the one-time wiring those packages' own doc
// comments describe (executeTransitionTx dispatching to registered
// TransitionHook/WorkflowHook implementations, GetDashboardKPIs
// aggregating registered ExternalKPIProvider/KPIProvider results) - this
// package itself only ever imports internal/plugin, never
// internal/workflow or internal/reports directly.
//
// It implements two of internal/plugin's three extension points:
//   - WorkflowHook: writes one activity_log row per executed transition -
//     migrations/0037's own activity_log table, deliberately separate
//     from audit_log (see that migration's own doc comment for why).
//   - KPIProvider: returns "activity_count_today", a count of this
//     firm's own activity_log rows created today.
//
// Not a ReportSource - nothing about this example plugin's own data
// needed a third demonstration; a real ReportSource-implementing plugin
// would follow the exact same "implement internal/plugin.ReportSource,
// register it in cmd/server/main.go" shape this file's own Register
// function establishes for the other two.
package activitylog

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	zdb "github.com/moonstreamtech/ZonaryOS/internal/platform/db"
	"github.com/moonstreamtech/ZonaryOS/internal/plugin"
)

// kpiKeyActivityCountToday is this plugin's one KPIProvider tile - see
// CONTRIBUTING.md's own "How to add a new KPI provider" section for why
// a plugin's own KPI key should be namespaced/distinctive enough not to
// collide with a future built-in kpiDescriptors key (internal/reports/kpi.go).
const kpiKeyActivityCountToday = "activity_count_today"

// Plugin holds the *pgxpool.Pool this plugin's own OnTransition writes
// through - injected at construction (New), not read from a package-level
// global, so a test can construct its own Plugin against a test pool
// without touching any process-wide state.
type Plugin struct {
	pool *pgxpool.Pool
}

// New returns a Plugin ready to register.
func New(pool *pgxpool.Pool) *Plugin {
	return &Plugin{pool: pool}
}

// Register wires p into registry as both a WorkflowHook and a
// KPIProvider - the one call cmd/server/main.go makes to enable this
// plugin (Part 1's plugins/firm_plugin_configs data model is this
// batch's own catalog/enablement story; Register is the actual Go-level
// "this plugin is compiled in and active" step Part 2's own "no dynamic
// loading, registered at compile time" scope boundary describes).
func Register(registry *plugin.Registry, pool *pgxpool.Pool) {
	p := New(pool)
	registry.RegisterWorkflowHook(p)
	registry.RegisterKPIProvider(p)
}

// OnTransition implements plugin.WorkflowHook - writes one human-readable
// activity_log row describing the transition that just committed.
//
// The description is built entirely from TransitionEvent's own fields
// (definition/state/action keys) - it does NOT name the acting user the
// way this batch's own illustrative "Kaan sold 5 units of Widget A"
// example does, because TransitionEvent (internal/plugin/hooks.go) - the
// interface this batch's own design brief specifies verbatim - carries
// no actor identity field at all, only FirmID/InstanceID/DefinitionKey/
// FromState/ToState/ActionKey/Payload. A future batch that wants
// per-user activity descriptions would need to add e.g. UserID to
// TransitionEvent itself (a core internal/plugin change, not something
// this example plugin can work around on its own) - documented here
// rather than silently faking a name.
func (p *Plugin) OnTransition(ctx context.Context, event plugin.TransitionEvent) error {
	description := fmt.Sprintf(
		"%s: %s -> %s (%s)",
		event.DefinitionKey, event.FromState, event.ToState, event.ActionKey,
	)

	return zdb.WithFirmContext(ctx, p.pool, event.FirmID, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO activity_log (firm_id, description, source_instance_id)
			VALUES ($1, $2, $3)
		`, event.FirmID, description, event.InstanceID)
		if err != nil {
			return fmt.Errorf("insert activity log entry: %w", err)
		}
		return nil
	})
}

// GetKPIs implements plugin.KPIProvider - one tile,
// "activity_count_today" (unitCount-shaped, matching
// internal/reports.KPIResult.Unit's own "count" convention).
//
// ciaudit:ignore-firmid-check: called only by
// internal/reports.GetDashboardKPIs, AFTER that function's own
// permission.IsMember check has already passed for firmID - a
// KPIProvider (internal/plugin/hooks.go's own interface, this batch's
// design brief verbatim) has no userID parameter to re-check
// membership with, the same "auth checked once at the entry boundary,
// not re-checked at every internal call" discipline this codebase's own
// *Tx helper functions already follow.
func (p *Plugin) GetKPIs(ctx context.Context, firmID uuid.UUID, pool *pgxpool.Pool) ([]plugin.KPIValue, error) {
	var count int
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM activity_log
			WHERE firm_id = $1 AND created_at >= date_trunc('day', now()) AND created_at < date_trunc('day', now()) + interval '1 day'
		`, firmID).Scan(&count)
	})
	if err != nil {
		return nil, fmt.Errorf("count today's activity log entries: %w", err)
	}
	return []plugin.KPIValue{
		{Key: kpiKeyActivityCountToday, Unit: "count", Value: fmt.Sprintf("%d", count)},
	}, nil
}

// ListToday returns today's activity_log entries for firmID, most recent
// first - not part of any internal/plugin extension point (there is no
// "read this plugin's own data" interface in this batch), just a plain
// exported helper a future admin UI or test could call directly against
// this plugin's own table. Unexported time.Now() call kept trivial/
// inlined (no separate "now" parameter) since this is example/
// demonstration code, not a KPI needing deterministic testability the
// way GetKPIs' own SQL-side date_trunc('day', now()) already is.
//
// ciaudit:ignore-firmid-check: not wired into any HTTP handler in this
// batch (there is no "read this plugin's own data" internal/plugin
// extension point) - a future caller that DOES expose this over HTTP
// must add its own permission.IsMember check before calling it, the same
// way every other *Tx/pool-taking read helper in this codebase documents
// that its own caller is responsible for authorization.
func ListToday(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID) ([]Entry, error) {
	var entries []Entry
	err := zdb.WithFirmContext(ctx, pool, firmID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, description, source_instance_id, created_at FROM activity_log
			WHERE firm_id = $1 AND created_at >= date_trunc('day', now())
			ORDER BY created_at DESC
		`, firmID)
		if err != nil {
			return fmt.Errorf("list today's activity log entries: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var e Entry
			if err := rows.Scan(&e.ID, &e.Description, &e.SourceInstanceID, &e.CreatedAt); err != nil {
				return fmt.Errorf("scan activity log entry: %w", err)
			}
			entries = append(entries, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// Entry is one activity_log row.
type Entry struct {
	ID               uuid.UUID
	Description      string
	SourceInstanceID *uuid.UUID
	CreatedAt        time.Time
}
