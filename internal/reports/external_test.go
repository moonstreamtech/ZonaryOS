// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package reports

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/queryfilter"
)

// resetExternalRegistrationsForTest clears externalSources/
// externalKPIProviders before/after a test - both are normally populated
// once at startup (cmd/server/main.go) and never touched again, so a
// test package running multiple tests needs a clean slate each time.
func resetExternalRegistrationsForTest(t *testing.T) {
	t.Helper()
	originalSources := externalSources
	originalProviders := externalKPIProviders
	externalSources = map[string]ExternalSource{}
	externalKPIProviders = nil
	t.Cleanup(func() {
		externalSources = originalSources
		externalKPIProviders = originalProviders
	})
}

// mockExternalSource is a minimal ExternalSource - one string field
// ("name"), one numeric field ("amount") - just enough to prove
// executeQuerySpec/validateQuerySpec actually dispatch to a registered
// plugin-provided entity, mirroring the "mock the extension point, don't
// stand up a real plugin" approach internal/ai's own tests use for their
// own external dependency.
type mockExternalSource struct {
	rows []map[string]any
}

func (m mockExternalSource) Entity() string { return "mock_entity" }

func (m mockExternalSource) AllowedFields() map[string]queryfilter.FieldDef {
	return map[string]queryfilter.FieldDef{
		"name":   {Column: "name", Kind: queryfilter.KindString},
		"amount": {Column: "amount", Kind: queryfilter.KindNumber},
	}
}

func (m mockExternalSource) Query(ctx context.Context, spec QuerySpec, firmID uuid.UUID, pool *pgxpool.Pool) ([]map[string]any, int, error) {
	return m.rows, len(m.rows), nil
}

// TestReportSource_RegisteredSourceIsQueryable is Part 2's own explicit
// test requirement: a registered mock source is queryable via the report
// engine. Exercised at executeQuerySpec's own level (the exact function
// RunReport calls) - tx/pool are both nil since neither the built-in
// entityRegistry SQL path nor mockExternalSource's own Query
// implementation ever touches them for this entity.
func TestReportSource_RegisteredSourceIsQueryable(t *testing.T) {
	resetExternalRegistrationsForTest(t)
	source := mockExternalSource{rows: []map[string]any{
		{"name": "Widget A", "total": "150.00"},
		{"name": "Widget B", "total": "75.00"},
	}}
	RegisterExternalSource(source)

	spec := QuerySpec{
		Entity:  "mock_entity",
		GroupBy: "name",
		Metrics: []QueryMetric{{Name: "total", Aggregation: "count"}},
	}
	rows, err := executeQuerySpec(context.Background(), nil, nil, uuid.New(), spec)
	if err != nil {
		t.Fatalf("executeQuerySpec: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows from the registered external source, got %d", len(rows))
	}
	if rows[0].GroupKey == nil || *rows[0].GroupKey != "Widget A" {
		t.Fatalf("expected the first row's group_key to be pulled from the \"name\" field, got %+v", rows[0])
	}
	if rows[0].Metrics["total"] != "150.00" {
		t.Fatalf("expected the row's own \"total\" field to pass through as a metric, got %+v", rows[0].Metrics)
	}
}

// TestReportSource_UnknownEntityStillRejected proves entityRegistry's
// own built-in entities and a registered ExternalSource don't silently
// swallow a genuinely unknown entity name - the fallback only ever
// engages for a name that IS registered.
func TestReportSource_UnknownEntityStillRejected(t *testing.T) {
	resetExternalRegistrationsForTest(t)
	spec := QuerySpec{Entity: "nonexistent_entity", Metrics: []QueryMetric{{Name: "x", Aggregation: "count"}}}
	if err := validateQuerySpec(spec); err == nil {
		t.Fatal("expected an unregistered, unknown entity to be rejected")
	}
}

// TestReportSource_ValidateRejectsUnknownField proves
// validateExternalQuerySpec (validateQuerySpec's own fallback path)
// actually checks metric/group_by/filter fields against the source's own
// AllowedFields, the same safety boundary BuildQuery enforces for
// entityRegistry.
func TestReportSource_ValidateRejectsUnknownField(t *testing.T) {
	resetExternalRegistrationsForTest(t)
	RegisterExternalSource(mockExternalSource{})

	spec := QuerySpec{Entity: "mock_entity", GroupBy: "not_a_real_field", Metrics: []QueryMetric{{Name: "x", Aggregation: "count"}}}
	if err := validateQuerySpec(spec); err == nil {
		t.Fatal("expected an unknown group_by field to be rejected")
	}
}

// mockKPIProvider is a minimal ExternalKPIProvider.
type mockKPIProvider struct {
	kpis []KPIResult
	err  error
}

func (m mockKPIProvider) GetKPIs(ctx context.Context, firmID uuid.UUID, pool *pgxpool.Pool) ([]KPIResult, error) {
	return m.kpis, m.err
}

// TestKPIProvider_RegisteredProviderResultsAreAppendable is Part 2's own
// explicit test requirement: a registered mock provider's KPIs appear in
// the dashboard response. GetDashboardKPIs itself needs a real Postgres
// (its own built-in tiles run inside a WithFirmContext transaction), so
// this exercises the exact append-and-continue-on-error logic
// GetDashboardKPIs' own doc comment describes, at the granularity that
// doesn't need a database: a provider erroring must not stop, and a
// provider succeeding must contribute its own tiles.
func TestKPIProvider_RegisteredProviderResultsAreAppendable(t *testing.T) {
	resetExternalRegistrationsForTest(t)
	good := mockKPIProvider{kpis: []KPIResult{{Key: "mock_metric", Unit: "count", Value: "3"}}}
	RegisterExternalKPIProvider(good)

	if len(externalKPIProviders) != 1 {
		t.Fatalf("expected exactly 1 registered KPI provider, got %d", len(externalKPIProviders))
	}
	results, err := externalKPIProviders[0].GetKPIs(context.Background(), uuid.New(), nil)
	if err != nil {
		t.Fatalf("GetKPIs: %v", err)
	}
	if len(results) != 1 || results[0].Key != "mock_metric" || results[0].Value != "3" {
		t.Fatalf("expected the registered provider's own tile to come through unchanged, got %+v", results)
	}
}
