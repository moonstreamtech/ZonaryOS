// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package reports

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/queryfilter"
)

// CompareOp, QueryFilter's Op vocabulary, is now internal/queryfilter's
// own CompareOp - kept as a type alias (not a redeclaration) so every
// existing QuerySpec.Filters caller/JSON shape is unchanged. Extracted
// into that dependency-free leaf package so list endpoints outside this
// package (internal/workflow, internal/accounting, internal/invoicing,
// internal/logistics) can reuse the exact same filter-clause-building
// logic for their own `?filters=` params without importing
// internal/reports itself - which would cycle, since internal/reports
// already imports internal/workflow and internal/accounting for its own
// KPI dashboard. See internal/queryfilter's own doc comment for the full
// reasoning.
type CompareOp = queryfilter.CompareOp

const (
	OpEq       = queryfilter.OpEq
	OpNeq      = queryfilter.OpNeq
	OpLt       = queryfilter.OpLt
	OpGt       = queryfilter.OpGt
	OpLte      = queryfilter.OpLte
	OpGte      = queryfilter.OpGte
	OpContains = queryfilter.OpContains
)

// fieldKind or fieldDef of THIS package's own entityRegistry are now type
// aliases over internal/queryfilter's exported equivalents, for the same
// reuse-without-coupling reason as CompareOp above - entityRegistry
// itself (below) stays private to this package; only the field-kind/def
// vocabulary is shared.
type fieldKind = queryfilter.FieldKind

const (
	fieldString    = queryfilter.KindString
	fieldNumber    = queryfilter.KindNumber
	fieldTimestamp = queryfilter.KindTimestamp
	fieldBoolean   = queryfilter.KindBoolean
	fieldUUID      = queryfilter.KindUUID
)

type fieldDef = queryfilter.FieldDef

type entityDef struct {
	// table is the real, trusted SQL table name.
	table  string
	fields map[string]fieldDef
}

// entityRegistry is the closed, hardcoded set of entities and fields a
// QuerySpec may reference - this IS the safety boundary: BuildQuery never
// accepts an entity/table/column name it didn't get from this map, so a
// malicious or malformed query_spec can reject with ErrInvalidQuerySpec,
// never reach raw SQL string interpolation of untrusted identifiers, and
// never disclose a column this map doesn't explicitly list (e.g. no
// tenant's data, no other module's internal columns). Every table listed
// here is firm-scoped (firm_id column) - BuildQuery always adds
// `firm_id = $1` regardless of what the caller's filters ask for.
var entityRegistry = map[string]entityDef{
	"workflow_instances": {
		table: "workflow_instances",
		fields: map[string]fieldDef{
			"id":                     {Column: "id", Kind: fieldUUID},
			"workflow_definition_id": {Column: "workflow_definition_id", Kind: fieldUUID},
			"created_by_user_id":     {Column: "created_by_user_id", Kind: fieldUUID},
			"created_at":             {Column: "created_at", Kind: fieldTimestamp},
			"updated_at":             {Column: "updated_at", Kind: fieldTimestamp},
		},
	},
	"journal_entries": {
		table: "journal_entries",
		fields: map[string]fieldDef{
			"id":          {Column: "id", Kind: fieldUUID},
			"description": {Column: "description", Kind: fieldString},
			"source_type": {Column: "source_type", Kind: fieldString},
			"source_id":   {Column: "source_id", Kind: fieldUUID},
			"created_by":  {Column: "created_by", Kind: fieldUUID},
			"posted_at":   {Column: "posted_at", Kind: fieldTimestamp},
			"created_at":  {Column: "created_at", Kind: fieldTimestamp},
		},
	},
	"invoices": {
		table: "invoices",
		fields: map[string]fieldDef{
			"id":             {Column: "id", Kind: fieldUUID},
			"invoice_number": {Column: "invoice_number", Kind: fieldString},
			"customer_id":    {Column: "customer_id", Kind: fieldUUID},
			"status":         {Column: "status", Kind: fieldString},
			"subtotal":       {Column: "subtotal", Kind: fieldNumber},
			"tax_amount":     {Column: "tax_amount", Kind: fieldNumber},
			"total":          {Column: "total", Kind: fieldNumber},
			"currency":       {Column: "currency", Kind: fieldString},
			"issued_date":    {Column: "issued_date", Kind: fieldTimestamp},
			"due_date":       {Column: "due_date", Kind: fieldTimestamp},
			"created_at":     {Column: "created_at", Kind: fieldTimestamp},
		},
	},
	"deliveries": {
		table: "deliveries",
		fields: map[string]fieldDef{
			"id":             {Column: "id", Kind: fieldUUID},
			"reference":      {Column: "reference", Kind: fieldString},
			"carrier":        {Column: "carrier", Kind: fieldString},
			"status":         {Column: "status", Kind: fieldString},
			"source_type":    {Column: "source_type", Kind: fieldString},
			"source_id":      {Column: "source_id", Kind: fieldUUID},
			"estimated_date": {Column: "estimated_date", Kind: fieldTimestamp},
			"actual_date":    {Column: "actual_date", Kind: fieldTimestamp},
			"created_at":     {Column: "created_at", Kind: fieldTimestamp},
		},
	},
	"people": {
		table: "people",
		fields: map[string]fieldDef{
			"id":         {Column: "id", Kind: fieldUUID},
			"full_name":  {Column: "full_name", Kind: fieldString},
			"type":       {Column: "type", Kind: fieldString},
			"email":      {Column: "email", Kind: fieldString},
			"status":     {Column: "status", Kind: fieldString},
			"start_date": {Column: "start_date", Kind: fieldTimestamp},
			"end_date":   {Column: "end_date", Kind: fieldTimestamp},
			"created_at": {Column: "created_at", Kind: fieldTimestamp},
		},
	},
	"products": {
		table: "products",
		fields: map[string]fieldDef{
			"id":         {Column: "id", Kind: fieldUUID},
			"sku":        {Column: "sku", Kind: fieldString},
			"name":       {Column: "name", Kind: fieldString},
			"unit":       {Column: "unit", Kind: fieldString},
			"unit_price": {Column: "unit_price", Kind: fieldNumber},
			"cost_price": {Column: "cost_price", Kind: fieldNumber},
			"category":   {Column: "category", Kind: fieldString},
			"is_active":  {Column: "is_active", Kind: fieldBoolean},
			"created_at": {Column: "created_at", Kind: fieldTimestamp},
		},
	},
}

// QuerySpec is a report_definitions.query_spec row's Go shape - a
// structured descriptor, not SQL (see this package's doc comment and
// migrations/0021's own comment on the same point).
type QuerySpec struct {
	Entity    string          `json:"entity"`
	Filters   []QueryFilter   `json:"filters,omitempty"`
	GroupBy   string          `json:"group_by,omitempty"`
	Metrics   []QueryMetric   `json:"metrics"`
	DateRange *QueryDateRange `json:"date_range,omitempty"`
}

// QueryFilter is one `{field, op, value}` leaf - now a type alias over
// internal/queryfilter.Filter (identical JSON shape, so every stored
// report_definitions.query_spec row still round-trips unchanged); see
// this file's own import comment for why the type lives there.
type QueryFilter = queryfilter.Filter

// QueryMetric is one requested aggregation - Aggregation is either the
// literal "count", or "sum(field)"/"avg(field)"/"min(field)"/"max(field)"
// naming an allowed numeric field of Entity.
type QueryMetric struct {
	Name        string `json:"name"`
	Aggregation string `json:"aggregation"`
}

// QueryDateRange narrows the query to [From, To] on Field - Field must be
// one of Entity's allowed timestamp fields.
type QueryDateRange struct {
	From  *time.Time `json:"from,omitempty"`
	To    *time.Time `json:"to,omitempty"`
	Field string     `json:"field"`
}

var aggregationPattern = regexp.MustCompile(`^(sum|avg|min|max)\(([a-zA-Z_][a-zA-Z0-9_]*)\)$`)

// builtQuery is BuildQuery's result: a fully parameterized SQL statement
// (every value bound via $N, every identifier drawn from entityRegistry)
// plus the ordered metric/group-by column labels the caller should map
// result rows back onto.
type BuiltQuery struct {
	SQL         string
	Args        []any
	metricNames []string
	hasGroupBy  bool
}

// BuildQuery translates spec into a safe, parameterized SQL query against
// firmID's data - the ONLY place a QuerySpec becomes SQL. Every table and
// column name in the generated SQL comes from entityRegistry (a closed,
// hardcoded allow-list per entity), never from spec directly; every
// filter/date-range value is bound via Postgres's own $N parameter
// placeholders, never string-interpolated. An entity, field, or
// aggregation not present in entityRegistry is rejected with
// ErrInvalidQuerySpec (mapped to HTTP 400 by the handler) rather than
// silently ignored or allowed through - this is what prevents both SQL
// injection (no untrusted string ever reaches the query text) and
// information disclosure (a field genuinely not on the allow-list can
// never be selected, filtered, or grouped on, regardless of what the
// request asks for).
//
// ciaudit:ignore-firmid-check: this function makes no database call at
// all - it only builds a SQL string and its bound args in memory. firmID
// is bound as the query's own `firm_id = $1` filter, not used to decide
// authorization; every caller (RunReport, validateQuerySpec) runs
// permission.IsMember before ever reaching this function.
func BuildQuery(firmID uuid.UUID, spec QuerySpec) (BuiltQuery, error) {
	entity, ok := entityRegistry[spec.Entity]
	if !ok {
		return BuiltQuery{}, fmt.Errorf("%w: unknown entity %q", ErrInvalidQuerySpec, spec.Entity)
	}
	if len(spec.Metrics) == 0 {
		return BuiltQuery{}, fmt.Errorf("%w: at least one metric is required", ErrInvalidQuerySpec)
	}

	args := []any{firmID}
	var selectExprs []string
	var metricNames []string

	hasGroupBy := spec.GroupBy != ""
	var groupByCol string
	if hasGroupBy {
		field, ok := entity.fields[spec.GroupBy]
		if !ok {
			return BuiltQuery{}, fmt.Errorf("%w: unknown group_by field %q for entity %q", ErrInvalidQuerySpec, spec.GroupBy, spec.Entity)
		}
		groupByCol = field.Column
		// ::text: a GROUP BY column can be any type entityRegistry allows
		// (uuid, timestamp, boolean, ...) - casting to text at the SQL
		// level means executeQuerySpec can always scan group_key into a
		// plain *string, regardless of the underlying column type.
		selectExprs = append(selectExprs, groupByCol+"::text AS group_key")
	}

	for _, m := range spec.Metrics {
		if strings.TrimSpace(m.Name) == "" {
			return BuiltQuery{}, fmt.Errorf("%w: metric name must not be empty", ErrInvalidQuerySpec)
		}
		if m.Aggregation == "count" {
			selectExprs = append(selectExprs, "count(*)")
			metricNames = append(metricNames, m.Name)
			continue
		}
		match := aggregationPattern.FindStringSubmatch(m.Aggregation)
		if match == nil {
			return BuiltQuery{}, fmt.Errorf("%w: unsupported aggregation %q", ErrInvalidQuerySpec, m.Aggregation)
		}
		fn, fieldKey := match[1], match[2]
		field, ok := entity.fields[fieldKey]
		if !ok {
			return BuiltQuery{}, fmt.Errorf("%w: unknown metric field %q for entity %q", ErrInvalidQuerySpec, fieldKey, spec.Entity)
		}
		if field.Kind != fieldNumber {
			return BuiltQuery{}, fmt.Errorf("%w: field %q does not support aggregation %q", ErrInvalidQuerySpec, fieldKey, fn)
		}
		selectExprs = append(selectExprs, fmt.Sprintf("%s(%s)", fn, field.Column))
		metricNames = append(metricNames, m.Name)
	}

	var whereClauses []string
	whereClauses = append(whereClauses, "firm_id = $1")

	// Delegates to internal/queryfilter.BuildClause - the same
	// filter-clause-building logic this function used to inline directly,
	// now shared with every other list endpoint's own `?filters=` param
	// (see this file's own import comment). entity.fields already IS a
	// map[string]queryfilter.FieldDef (fieldDef is a type alias), so no
	// conversion is needed.
	filterClauses, newArgs, err := queryfilter.BuildClause(entity.fields, spec.Filters, args)
	if err != nil {
		return BuiltQuery{}, fmt.Errorf("%w: %w", ErrInvalidQuerySpec, err)
	}
	args = newArgs
	whereClauses = append(whereClauses, filterClauses...)

	if spec.DateRange != nil {
		field, ok := entity.fields[spec.DateRange.Field]
		if !ok {
			return BuiltQuery{}, fmt.Errorf("%w: unknown date_range field %q for entity %q", ErrInvalidQuerySpec, spec.DateRange.Field, spec.Entity)
		}
		if field.Kind != fieldTimestamp {
			return BuiltQuery{}, fmt.Errorf("%w: date_range field %q is not a timestamp field", ErrInvalidQuerySpec, spec.DateRange.Field)
		}
		if spec.DateRange.From != nil {
			args = append(args, *spec.DateRange.From)
			whereClauses = append(whereClauses, fmt.Sprintf("%s >= $%d", field.Column, len(args)))
		}
		if spec.DateRange.To != nil {
			args = append(args, *spec.DateRange.To)
			whereClauses = append(whereClauses, fmt.Sprintf("%s <= $%d", field.Column, len(args)))
		}
	}

	sql := "SELECT " + strings.Join(selectExprs, ", ") + " FROM " + entity.table +
		" WHERE " + strings.Join(whereClauses, " AND ")
	if hasGroupBy {
		sql += " GROUP BY " + groupByCol
	}

	return BuiltQuery{SQL: sql, Args: args, metricNames: metricNames, hasGroupBy: hasGroupBy}, nil
}
