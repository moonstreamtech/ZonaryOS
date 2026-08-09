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
)

// CompareOp is a report filter's comparison operator - the same closed
// vocabulary internal/workflow.CompareOp uses for rule condition leaves
// (eq/neq/lt/gt/lte/gte/contains), redeclared here rather than imported
// cross-package (this package already redeclares plenty of its own small
// closed vocabularies, e.g. Period/KPIKind - one more is consistent, not
// a new pattern).
type CompareOp string

const (
	OpEq       CompareOp = "eq"
	OpNeq      CompareOp = "neq"
	OpLt       CompareOp = "lt"
	OpGt       CompareOp = "gt"
	OpLte      CompareOp = "lte"
	OpGte      CompareOp = "gte"
	OpContains CompareOp = "contains"
)

var sqlCompareOp = map[CompareOp]string{
	OpEq:  "=",
	OpNeq: "<>",
	OpLt:  "<",
	OpGt:  ">",
	OpLte: "<=",
	OpGte: ">=",
	// OpContains is handled separately (ILIKE), not through this map.
}

// fieldKind is a report field's value type - constrains which CompareOps
// and aggregations are valid against it.
type fieldKind string

const (
	fieldString    fieldKind = "string"
	fieldNumber    fieldKind = "number"
	fieldTimestamp fieldKind = "timestamp"
	fieldBoolean   fieldKind = "boolean"
	fieldUUID      fieldKind = "uuid"
)

type fieldDef struct {
	// column is the real, trusted SQL column name - the only thing ever
	// substituted into a query string for this field; it never comes from
	// request input directly, only ever looked up by key in entityDef.fields.
	column string
	kind   fieldKind
}

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
			"id":                     {"id", fieldUUID},
			"workflow_definition_id": {"workflow_definition_id", fieldUUID},
			"created_by_user_id":     {"created_by_user_id", fieldUUID},
			"created_at":             {"created_at", fieldTimestamp},
			"updated_at":             {"updated_at", fieldTimestamp},
		},
	},
	"journal_entries": {
		table: "journal_entries",
		fields: map[string]fieldDef{
			"id":          {"id", fieldUUID},
			"description": {"description", fieldString},
			"source_type": {"source_type", fieldString},
			"source_id":   {"source_id", fieldUUID},
			"created_by":  {"created_by", fieldUUID},
			"posted_at":   {"posted_at", fieldTimestamp},
			"created_at":  {"created_at", fieldTimestamp},
		},
	},
	"invoices": {
		table: "invoices",
		fields: map[string]fieldDef{
			"id":             {"id", fieldUUID},
			"invoice_number": {"invoice_number", fieldString},
			"customer_id":    {"customer_id", fieldUUID},
			"status":         {"status", fieldString},
			"subtotal":       {"subtotal", fieldNumber},
			"tax_amount":     {"tax_amount", fieldNumber},
			"total":          {"total", fieldNumber},
			"currency":       {"currency", fieldString},
			"issued_date":    {"issued_date", fieldTimestamp},
			"due_date":       {"due_date", fieldTimestamp},
			"created_at":     {"created_at", fieldTimestamp},
		},
	},
	"deliveries": {
		table: "deliveries",
		fields: map[string]fieldDef{
			"id":             {"id", fieldUUID},
			"reference":      {"reference", fieldString},
			"carrier":        {"carrier", fieldString},
			"status":         {"status", fieldString},
			"source_type":    {"source_type", fieldString},
			"source_id":      {"source_id", fieldUUID},
			"estimated_date": {"estimated_date", fieldTimestamp},
			"actual_date":    {"actual_date", fieldTimestamp},
			"created_at":     {"created_at", fieldTimestamp},
		},
	},
	"people": {
		table: "people",
		fields: map[string]fieldDef{
			"id":         {"id", fieldUUID},
			"full_name":  {"full_name", fieldString},
			"type":       {"type", fieldString},
			"email":      {"email", fieldString},
			"status":     {"status", fieldString},
			"start_date": {"start_date", fieldTimestamp},
			"end_date":   {"end_date", fieldTimestamp},
			"created_at": {"created_at", fieldTimestamp},
		},
	},
	"products": {
		table: "products",
		fields: map[string]fieldDef{
			"id":         {"id", fieldUUID},
			"sku":        {"sku", fieldString},
			"name":       {"name", fieldString},
			"unit":       {"unit", fieldString},
			"unit_price": {"unit_price", fieldNumber},
			"cost_price": {"cost_price", fieldNumber},
			"category":   {"category", fieldString},
			"is_active":  {"is_active", fieldBoolean},
			"created_at": {"created_at", fieldTimestamp},
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

// QueryFilter is one `{field, op, value}` leaf - same field/op vocabulary
// internal/workflow's rule condition leaves use (see CompareOp above).
type QueryFilter struct {
	Field string    `json:"field"`
	Op    CompareOp `json:"op"`
	Value any       `json:"value"`
}

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
		groupByCol = field.column
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
		if field.kind != fieldNumber {
			return BuiltQuery{}, fmt.Errorf("%w: field %q does not support aggregation %q", ErrInvalidQuerySpec, fieldKey, fn)
		}
		selectExprs = append(selectExprs, fmt.Sprintf("%s(%s)", fn, field.column))
		metricNames = append(metricNames, m.Name)
	}

	var whereClauses []string
	whereClauses = append(whereClauses, "firm_id = $1")

	for _, f := range spec.Filters {
		field, ok := entity.fields[f.Field]
		if !ok {
			return BuiltQuery{}, fmt.Errorf("%w: unknown filter field %q for entity %q", ErrInvalidQuerySpec, f.Field, spec.Entity)
		}
		if f.Op == OpContains && field.kind != fieldString {
			return BuiltQuery{}, fmt.Errorf("%w: op %q is only valid on string fields", ErrInvalidQuerySpec, f.Op)
		}
		switch f.Op {
		case OpEq, OpNeq, OpLt, OpGt, OpLte, OpGte:
			sqlOp, ok := sqlCompareOp[f.Op]
			if !ok {
				return BuiltQuery{}, fmt.Errorf("%w: unsupported op %q", ErrInvalidQuerySpec, f.Op)
			}
			args = append(args, f.Value)
			whereClauses = append(whereClauses, fmt.Sprintf("%s %s $%d", field.column, sqlOp, len(args)))
		case OpContains:
			s, ok := f.Value.(string)
			if !ok {
				return BuiltQuery{}, fmt.Errorf("%w: op %q requires a string value", ErrInvalidQuerySpec, f.Op)
			}
			args = append(args, "%"+s+"%")
			whereClauses = append(whereClauses, fmt.Sprintf("%s ILIKE $%d", field.column, len(args)))
		default:
			return BuiltQuery{}, fmt.Errorf("%w: unsupported op %q", ErrInvalidQuerySpec, f.Op)
		}
	}

	if spec.DateRange != nil {
		field, ok := entity.fields[spec.DateRange.Field]
		if !ok {
			return BuiltQuery{}, fmt.Errorf("%w: unknown date_range field %q for entity %q", ErrInvalidQuerySpec, spec.DateRange.Field, spec.Entity)
		}
		if field.kind != fieldTimestamp {
			return BuiltQuery{}, fmt.Errorf("%w: date_range field %q is not a timestamp field", ErrInvalidQuerySpec, spec.DateRange.Field)
		}
		if spec.DateRange.From != nil {
			args = append(args, *spec.DateRange.From)
			whereClauses = append(whereClauses, fmt.Sprintf("%s >= $%d", field.column, len(args)))
		}
		if spec.DateRange.To != nil {
			args = append(args, *spec.DateRange.To)
			whereClauses = append(whereClauses, fmt.Sprintf("%s <= $%d", field.column, len(args)))
		}
	}

	sql := "SELECT " + strings.Join(selectExprs, ", ") + " FROM " + entity.table +
		" WHERE " + strings.Join(whereClauses, " AND ")
	if hasGroupBy {
		sql += " GROUP BY " + groupByCol
	}

	return BuiltQuery{SQL: sql, Args: args, metricNames: metricNames, hasGroupBy: hasGroupBy}, nil
}
