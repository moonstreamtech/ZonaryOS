// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Plugin/extension architecture + developer API/SDK foundation, Part 2:
// the two extension points internal/plugin's ReportSource/KPIProvider
// interfaces plug into. Defined HERE, in internal/reports, rather than
// this package importing internal/plugin's own interfaces of the same
// shape, specifically to avoid an import cycle: internal/plugin already
// imports this package (for QuerySpec, ReportSource's own parameter
// type), so this package cannot import internal/plugin back.
//
// The resolution is Go's own structural typing: ExternalSource/
// ExternalKPIProvider below have method sets IDENTICAL to
// internal/plugin.ReportSource/KPIProvider (same parameter and return
// types, including the KPIValue = KPIResult type alias
// internal/plugin.KPIValue is defined as) - so any concrete type that
// implements internal/plugin's interfaces automatically implements these
// too, with zero adapter code. cmd/server/main.go is the one place that
// bridges the two: it reads internal/plugin.Registry's own registered
// slices and calls RegisterExternalSource/RegisterExternalKPIProvider
// for each, relying on exactly this structural match.
package reports

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moonstreamtech/ZonaryOS/internal/queryfilter"
)

// ExternalSource is a report entity contributed by a plugin, queryable
// through the same GET .../reports/definitions/{firmID}/{id}/run path as
// any built-in entityRegistry entity - see executeQuerySpec's own doc
// comment for exactly where the fallback happens.
type ExternalSource interface {
	Entity() string
	AllowedFields() map[string]queryfilter.FieldDef
	Query(ctx context.Context, spec QuerySpec, firmID uuid.UUID, pool *pgxpool.Pool) ([]map[string]any, int, error)
}

// ExternalKPIProvider is a set of dashboard tiles contributed by a
// plugin, aggregated alongside the built-in kpiDescriptors - see
// GetDashboardKPIs' own doc comment for exactly where these are called.
type ExternalKPIProvider interface {
	GetKPIs(ctx context.Context, firmID uuid.UUID, pool *pgxpool.Pool) ([]KPIResult, error)
}

// externalSources/externalKPIProviders are populated exactly once, at
// startup (cmd/server/main.go, immediately after internal/plugin.Registry
// itself is populated) - not protected by a mutex, the same "write once
// before any request is served, read-only for the rest of the process
// lifetime" contract internal/plugin.Registry's own doc comment
// describes for its own slices (Part 2's "immutable after init, no
// hot-reloading" scope boundary).
var (
	externalSources      = map[string]ExternalSource{}
	externalKPIProviders []ExternalKPIProvider
)

// RegisterExternalSource adds source to the report engine's own entity
// lookup, keyed by source.Entity() - called once per plugin-provided
// report source at startup. A name colliding with a built-in
// entityRegistry key is deliberately NOT an error here (this function
// has no way to fail loudly at plugin-registration time in a way
// cmd/server/main.go could act on) - see executeQuerySpec's own doc
// comment for why entityRegistry always wins such a collision in
// practice (checked first).
func RegisterExternalSource(source ExternalSource) {
	externalSources[source.Entity()] = source
}

// RegisterExternalKPIProvider adds provider to the set GetDashboardKPIs
// aggregates alongside the built-in kpiDescriptors.
func RegisterExternalKPIProvider(provider ExternalKPIProvider) {
	externalKPIProviders = append(externalKPIProviders, provider)
}

// validateExternalQuerySpec checks spec against source's own
// AllowedFields - the exact same safety-boundary checks BuildQuery runs
// against entityRegistry (unknown field/aggregation/group_by/date_range
// field all rejected with ErrInvalidQuerySpec), reusing
// internal/queryfilter.BuildClause for the filters half (the same
// delegation BuildQuery itself already uses) rather than duplicating
// that logic a second time. This never builds or executes SQL itself -
// source.Query is responsible for actually running the query, in
// whatever way it likes; this function only validates spec is safe to
// hand to it.
func validateExternalQuerySpec(spec QuerySpec, source ExternalSource) error {
	fields := source.AllowedFields()
	if len(spec.Metrics) == 0 {
		return fmt.Errorf("%w: at least one metric is required", ErrInvalidQuerySpec)
	}
	for _, m := range spec.Metrics {
		if m.Name == "" {
			return fmt.Errorf("%w: metric name must not be empty", ErrInvalidQuerySpec)
		}
		if m.Aggregation == "count" {
			continue
		}
		match := aggregationPattern.FindStringSubmatch(m.Aggregation)
		if match == nil {
			return fmt.Errorf("%w: unsupported aggregation %q", ErrInvalidQuerySpec, m.Aggregation)
		}
		fn, fieldKey := match[1], match[2]
		field, ok := fields[fieldKey]
		if !ok {
			return fmt.Errorf("%w: unknown metric field %q for entity %q", ErrInvalidQuerySpec, fieldKey, spec.Entity)
		}
		if field.Kind != fieldNumber {
			return fmt.Errorf("%w: field %q does not support aggregation %q", ErrInvalidQuerySpec, fieldKey, fn)
		}
	}
	if spec.GroupBy != "" {
		if _, ok := fields[spec.GroupBy]; !ok {
			return fmt.Errorf("%w: unknown group_by field %q for entity %q", ErrInvalidQuerySpec, spec.GroupBy, spec.Entity)
		}
	}
	if spec.DateRange != nil {
		field, ok := fields[spec.DateRange.Field]
		if !ok {
			return fmt.Errorf("%w: unknown date_range field %q for entity %q", ErrInvalidQuerySpec, spec.DateRange.Field, spec.Entity)
		}
		if field.Kind != fieldTimestamp {
			return fmt.Errorf("%w: date_range field %q is not a timestamp field", ErrInvalidQuerySpec, spec.DateRange.Field)
		}
	}
	if _, _, err := queryfilter.BuildClause(fields, spec.Filters, []any{}); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidQuerySpec, err)
	}
	return nil
}

// executeExternalQuerySpec is executeQuerySpec's plugin-provided-entity
// path: validate, then delegate entirely to source.Query - see this
// package's own doc comment for the import-cycle-avoiding reasoning
// behind ExternalSource living here rather than internal/plugin defining
// it. rowsFromMaps adapts source.Query's generic []map[string]any rows
// (it has no notion of this package's own ReportRow/group-key shape)
// into the same ReportRow the built-in entityRegistry path returns, so a
// report definition's frontend rendering never needs to know whether a
// given row came from a built-in or a plugin-provided entity.
//
// ciaudit:ignore-firmid-check: internal helper called only by
// executeQuerySpec, itself only called by RunReport after RunReport's
// own permission.IsMember has already run - firmID here scopes the query
// (defense in depth), it is not itself an authorization decision, the
// same reasoning executeQuerySpec's own (pre-existing) ciaudit exemption
// gives for the built-in entityRegistry path.
func executeExternalQuerySpec(ctx context.Context, pool *pgxpool.Pool, firmID uuid.UUID, spec QuerySpec, source ExternalSource) ([]ReportRow, error) {
	if err := validateExternalQuerySpec(spec, source); err != nil {
		return nil, err
	}
	rows, _, err := source.Query(ctx, spec, firmID, pool)
	if err != nil {
		return nil, fmt.Errorf("query external report source %q: %w", spec.Entity, err)
	}
	return rowsFromMaps(rows, spec.GroupBy), nil
}

// rowsFromMaps converts source.Query's raw row maps into ReportRow -
// groupByField's own value (if spec.GroupBy was set) is pulled out as
// GroupKey (stringified, matching BuildQuery's own `::text AS group_key`
// convention for the built-in path), everything else in the map becomes
// a metric.
func rowsFromMaps(rows []map[string]any, groupByField string) []ReportRow {
	result := make([]ReportRow, 0, len(rows))
	for _, row := range rows {
		var groupKey *string
		metrics := make(map[string]any, len(row))
		for k, v := range row {
			if groupByField != "" && k == groupByField {
				s := fmt.Sprintf("%v", v)
				groupKey = &s
				continue
			}
			metrics[k] = v
		}
		result = append(result, ReportRow{GroupKey: groupKey, Metrics: metrics})
	}
	return result
}
