// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Package queryfilter is the `{field, op, value}` filter vocabulary
// internal/reports.QuerySpec.Filters already established, factored out
// into its own dependency-free leaf package so list endpoints outside
// internal/reports can reuse the exact same safe-SQL-building logic
// without duplicating it.
//
// Why a leaf package rather than importing internal/reports directly:
// internal/reports already imports internal/workflow and
// internal/accounting (for its KPI dashboard) - if internal/workflow or
// internal/accounting imported internal/reports back to reuse its filter
// logic, that would be an import cycle. Every package in this codebase
// can safely import internal/queryfilter (it imports nothing from this
// codebase itself), including internal/reports, which now builds its own
// QuerySpec.Filters handling on top of it instead of duplicating the
// switch-on-op logic.
//
// Same safety discipline BuildQuery's own doc comment establishes: the
// caller supplies a closed, hardcoded allow-list of field name -> real
// SQL column (never anything from request input), and every filter VALUE
// is bound via Postgres's own $N parameter placeholders, never
// interpolated into the query string. A filter field/op not present in
// the caller's allow-list is rejected with ErrInvalidFilter rather than
// silently ignored.
package queryfilter

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrInvalidFilter means a filter referenced a field/op this caller's own
// field allow-list doesn't recognize, or supplied a value of the wrong
// shape for the requested op (e.g. a non-string value with OpContains).
var ErrInvalidFilter = errors.New("invalid filter")

// CompareOp is a filter leaf's comparison operator - the same closed
// vocabulary internal/workflow's rule condition leaves and
// internal/reports.QueryFilter already use (eq/neq/lt/gt/lte/gte/contains).
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

// FieldKind is a filterable field's value type - constrains which
// CompareOps are valid against it (today, just OpContains requiring
// KindString).
type FieldKind string

const (
	KindString    FieldKind = "string"
	KindNumber    FieldKind = "number"
	KindTimestamp FieldKind = "timestamp"
	KindBoolean   FieldKind = "boolean"
	KindUUID      FieldKind = "uuid"
)

// FieldDef is one entry in a caller's own field allow-list: the real,
// trusted SQL column name (never taken from request input - only ever
// looked up by the request's field key against this map) plus its kind.
type FieldDef struct {
	Column string
	Kind   FieldKind
}

// Filter is one `{field, op, value}` leaf - the JSON shape
// `?filters=[...]` list-endpoint query params decode into, and the same
// shape internal/reports.QueryFilter carries (kept as a type alias there,
// see queryspec.go).
type Filter struct {
	Field string    `json:"field"`
	Op    CompareOp `json:"op"`
	Value any       `json:"value"`
}

// ParseFiltersParam decodes a `?filters=` query param's raw JSON (a
// `[{field, op, value}, ...]` array, the same shape
// internal/reports.QuerySpec.Filters stores) into a []Filter - the one
// place every list-endpoint handler that accepts `?filters=` decodes it,
// instead of each package repeating its own json.Unmarshal + error
// wrapping. An empty raw string returns (nil, nil) - "no filters
// requested", not an error.
func ParseFiltersParam(raw string) ([]Filter, error) {
	if raw == "" {
		return nil, nil
	}
	var filters []Filter
	if err := json.Unmarshal([]byte(raw), &filters); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidFilter, err)
	}
	return filters, nil
}

// BuildClause translates filters into SQL WHERE-clause fragments against
// fields (the caller's own closed allow-list of column name + kind),
// appending each filter's bound value to args (starting after whatever
// the caller already put there, so this composes with a query that
// already has its own leading params like `firm_id = $1`) and returning
// clauses the caller AND-joins into its own WHERE clause. Never mutates
// the fields map or reaches into any database - purely a string/args
// builder, same "no database call, no authorization decision" shape
// internal/reports.BuildQuery documents for itself.
func BuildClause(fields map[string]FieldDef, filters []Filter, args []any) (clauses []string, newArgs []any, err error) {
	newArgs = args
	for _, f := range filters {
		field, ok := fields[f.Field]
		if !ok {
			return nil, nil, fmt.Errorf("%w: unknown filter field %q", ErrInvalidFilter, f.Field)
		}
		if f.Op == OpContains && field.Kind != KindString {
			return nil, nil, fmt.Errorf("%w: op %q is only valid on string fields", ErrInvalidFilter, f.Op)
		}
		switch f.Op {
		case OpEq, OpNeq, OpLt, OpGt, OpLte, OpGte:
			sqlOp, ok := sqlCompareOp[f.Op]
			if !ok {
				return nil, nil, fmt.Errorf("%w: unsupported op %q", ErrInvalidFilter, f.Op)
			}
			newArgs = append(newArgs, f.Value)
			clauses = append(clauses, fmt.Sprintf("%s %s $%d", field.Column, sqlOp, len(newArgs)))
		case OpContains:
			s, ok := f.Value.(string)
			if !ok {
				return nil, nil, fmt.Errorf("%w: op %q requires a string value", ErrInvalidFilter, f.Op)
			}
			newArgs = append(newArgs, "%"+s+"%")
			clauses = append(clauses, fmt.Sprintf("%s ILIKE $%d", field.Column, len(newArgs)))
		default:
			return nil, nil, fmt.Errorf("%w: unsupported op %q", ErrInvalidFilter, f.Op)
		}
	}
	return clauses, newArgs, nil
}
