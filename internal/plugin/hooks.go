// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package plugin is the plugin/extension architecture + developer API/SDK
// foundation: three extension points a compiled-in Go plugin can
// implement (WorkflowHook, KPIProvider, ReportSource), and Registry, the
// in-process registration point every extension point dispatches
// through. See internal/plugins/activitylog for a real, working example
// plugin implementing WorkflowHook and KPIProvider without touching any
// core package.
//
// Scope boundaries (explicit, per this batch's own brief): no dynamic
// plugin loading - no .so files (Go's own plugin package has notoriously
// fragile cross-build-environment compatibility, exactly the operational
// risk this scope boundary avoids), no WASM. Every plugin is a Go package
// registered at compile time (cmd/server/main.go), the same way every
// other module in this modular monolith already registers its own
// RegisterRoutes. No plugin marketplace or installation UI. No plugin
// sandboxing/isolation - a registered plugin is trusted code with the
// same privileges as any other package in this binary, explicitly NOT
// subject to the PII-firewall discipline internal/ai's own provider
// calls follow (see TransitionEvent's own doc comment). No hot-reload.
// No plugin versioning beyond migrations/0037's plugins.version column
// (informational only - nothing in this package reads or enforces it).
package plugin

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/queryfilter"
	"github.com/moonstreamtech/ZonaryOS/internal/reports"
	"github.com/moonstreamtech/ZonaryOS/internal/workflow"
)

// TransitionEvent is what WorkflowHook.OnTransition receives - one
// executed workflow transition, already committed to the database by the
// time any hook sees it. A type ALIAS (not a redeclared struct) for
// internal/workflow.TransitionHookEvent - see that package's own
// plugin_hooks.go doc comment for why the canonical type/interface pair
// lives there (import-cycle avoidance: this package already imports
// internal/reports, which imports internal/workflow, so this package
// importing internal/workflow directly is fine, but internal/workflow
// importing this package back would complete a cycle). The alias means a
// concrete type implementing WorkflowHook below also structurally
// satisfies workflow.TransitionHook, with zero adapter code -
// cmd/server/main.go relies on exactly that when it bridges
// Registry.workflowHooks into workflow.RegisterTransitionHook.
//
// Payload carries the instance's merged payload verbatim, unlike
// internal/ai's own anomaly-detection prompt (which deliberately strips
// everything down to aggregated numbers before it ever reaches an
// external AI provider). That PII-firewall discipline does not apply
// here: a registered plugin is trusted, in-process Go code, not a
// third-party network endpoint - the same trust boundary this package's
// own doc comment draws for "no sandboxing/isolation."
type TransitionEvent = workflow.TransitionHookEvent

// WorkflowHook is called after a workflow transition executes (and
// commits) - see internal/plugins/activitylog's own OnTransition for a
// real implementation.
type WorkflowHook interface {
	OnTransition(ctx context.Context, event TransitionEvent) error
}

// KPIValue is one dashboard tile a KPIProvider contributes - a type
// ALIAS (not a redeclared struct) for internal/reports.KPIResult, so a
// concrete type implementing KPIProvider's GetKPIs also structurally
// satisfies internal/reports.ExternalKPIProvider's identical method
// signature without any adapter - see
// internal/reports.RegisterExternalKPIProvider's own doc comment for why
// that matters (the import-cycle-avoiding wiring this package's own
// ReportSource doc comment describes).
type KPIValue = reports.KPIResult

// KPIProvider contributes additional dashboard tiles alongside the
// built-in kpiDescriptors (internal/reports/kpi.go) - see
// internal/reports.RegisterExternalKPIProvider's own doc comment for how
// GetDashboardKPIs aggregates these in without internal/reports importing
// this package (which would cycle, since this package imports
// internal/reports for QuerySpec below).
//
// Takes pool, not an open tx: unlike the built-in KPIs (which all run
// inside GetDashboardKPIs' own already-open, RLS-scoped transaction), a
// plugin's GetKPIs call runs its own separate transaction(s) via pool -
// see internal/reports.GetDashboardKPIs' own doc comment for why (a
// plugin's own queries needn't share the built-ins' transaction
// boundary, and requiring it would leak an internal/reports
// implementation detail across this interface).
type KPIProvider interface {
	GetKPIs(ctx context.Context, firmID uuid.UUID, pool *pgxpool.Pool) ([]KPIValue, error)
}

// ReportSource contributes an additional entity the parametric report
// engine (internal/reports.BuildQuery/RunReport) can query, alongside the
// built-in, hardcoded entityRegistry - see
// internal/reports.RegisterExternalSource's own doc comment for the
// import-cycle-avoiding wiring (this interface's method set is
// structurally identical to internal/reports.ExternalSource, so any
// concrete type implementing this one automatically satisfies that one
// too, Go's usual duck typing - no adapter needed).
type ReportSource interface {
	// Entity is the entity name a QuerySpec.Entity value selects this
	// source with - must not collide with a built-in entityRegistry key
	// (internal/reports.RegisterExternalSource's own doc comment covers
	// the collision case).
	Entity() string
	// AllowedFields is this entity's own field allow-list - the same
	// safety-boundary role internal/reports' own entityRegistry[...].fields
	// plays for the built-in entities (see BuildQuery's own doc comment):
	// a field/aggregation not present here is rejected, never passed
	// through to whatever query this source builds internally.
	AllowedFields() map[string]queryfilter.FieldDef
	// Query runs spec (already validated against AllowedFields by the
	// caller) against firmID's data and returns the matching rows plus a
	// total count (mirroring BuiltQuery's own row-count contract) - takes
	// pool, not an open tx, same reasoning as KPIProvider.GetKPIs above.
	Query(ctx context.Context, spec reports.QuerySpec, firmID uuid.UUID, pool *pgxpool.Pool) ([]map[string]any, int, error)
}
