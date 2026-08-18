// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Part 4 of the data pipeline/ETL/system integrations batch: a small,
// composable transform pipeline for field_mappings' own "transform" spec.
// New transforms are one new case in applyOne's own switch - documented
// here for anyone extending this (a future plugin author, or a future
// batch) to follow: add the case, document what it does and what its
// argument (if any) means, add it to the doc comment list below.
package integration

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TransformChain is field_mappings[].transform's own JSON shape - either
// a single transform name ("uppercase") or an ordered array of them
// (["trim","uppercase"]), per this batch's own design brief. Custom
// (Un)MarshalJSON accepts/produces either shape, so a stored
// integration_mappings.field_mappings row round-trips exactly as
// authored (a single-transform mapping doesn't silently become an array
// on the next read).
type TransformChain []string

func (t *TransformChain) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if single == "" {
			*t = nil
			return nil
		}
		*t = TransformChain{single}
		return nil
	}
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return fmt.Errorf("transform must be a string or an array of strings: %w", err)
	}
	*t = multiple
	return nil
}

func (t TransformChain) MarshalJSON() ([]byte, error) {
	if len(t) == 1 {
		return json.Marshal(t[0])
	}
	return json.Marshal([]string(t))
}

// FieldMapping is one entry of integration_mappings.field_mappings.
type FieldMapping struct {
	SourceField string         `json:"source_field"`
	TargetField string         `json:"target_field"`
	Transform   TransformChain `json:"transform,omitempty"`
}

// ApplyFieldMappings builds the target-shaped record from source (one
// entity's own field->value map, either ZonaryOS's own data going out
// (outbound.go) or a partner system's raw JSON record coming in
// (inbound.go)) by walking mappings in order: for each, read
// source[m.SourceField] (nil if absent - "static" is the one transform
// that still produces a value from nothing), run it through
// ApplyTransforms, and set the result at result[m.TargetField].
func ApplyFieldMappings(source map[string]any, mappings []FieldMapping) (map[string]any, error) {
	result := make(map[string]any, len(mappings))
	for _, m := range mappings {
		value, err := ApplyTransforms(source[m.SourceField], m.Transform)
		if err != nil {
			return nil, fmt.Errorf("field %q -> %q: %w", m.SourceField, m.TargetField, err)
		}
		result[m.TargetField] = value
	}
	return result, nil
}

// ApplyTransforms runs each transform in chain against value in order,
// chaining output to input - transforms=["trim","uppercase"] runs
// "uppercase" on "trim"'s own output, not both independently on the
// original value.
func ApplyTransforms(value any, chain TransformChain) (any, error) {
	for _, spec := range chain {
		v, err := applyOne(value, spec)
		if err != nil {
			return nil, err
		}
		value = v
	}
	return value, nil
}

// applyOne applies a single transform spec ("name" or "name:arg") to
// value. Supported transforms (this batch's own full list - see this
// file's own doc comment for how to add one):
//   - "trim": strings.TrimSpace.
//   - "uppercase"/"lowercase": strings.ToUpper/ToLower.
//   - "multiply:N": value * N (N a decimal, e.g. "multiply:1.2") - value
//     must itself be numeric (a JSON number, or a numeric string).
//   - "date_format:LAYOUT": reformats a date value into LAYOUT, where
//     LAYOUT uses the same YYYY/MM/DD/HH/mm/ss tokens the field_mappings
//     JSON example in this batch's own design brief uses (translated to
//     Go's reference-time layout internally - see dateFormatLayout).
//     value must parse as RFC3339 or a plain YYYY-MM-DD date first (the
//     two formats this batch's own connectors are expected to send/
//     receive); a value in some other format is a real error, not a
//     silent pass-through.
//   - "static:VALUE": ignores value entirely, always returns VALUE - the
//     one transform where an absent/nil source field is not an error.
func applyOne(value any, spec string) (any, error) {
	name, arg, _ := strings.Cut(spec, ":")
	switch name {
	case "trim":
		return strings.TrimSpace(toString(value)), nil
	case "uppercase":
		return strings.ToUpper(toString(value)), nil
	case "lowercase":
		return strings.ToLower(toString(value)), nil
	case "multiply":
		factor, err := strconv.ParseFloat(arg, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: multiply requires a numeric argument, got %q", ErrInvalidTransform, arg)
		}
		num, err := toFloat(value)
		if err != nil {
			return nil, fmt.Errorf("%w: multiply: %v", ErrInvalidTransform, err)
		}
		return num * factor, nil
	case "date_format":
		return formatDate(value, arg)
	case "static":
		return arg, nil
	default:
		return nil, fmt.Errorf("%w: unknown transform %q", ErrInvalidTransform, name)
	}
}

// validateTransformSpec checks spec names a known transform with a
// syntactically valid argument (where one is required), WITHOUT
// executing it against any value - used by mappings.go's own
// validateMappingInput to reject an unknown/malformed transform at
// mapping-save time, the same "fail fast, at the boundary" reasoning
// internal/reports.validateQuerySpec's own doc comment gives. Kept
// separate from applyOne (which needs a real value to run against)
// specifically so validation doesn't require fabricating a placeholder
// value that might itself be wrongly rejected by a value-shape check
// (e.g. date_format's own "value must be non-empty" rule) unrelated to
// whether the transform spec ITSELF is well-formed.
func validateTransformSpec(spec string) error {
	name, arg, hasArg := strings.Cut(spec, ":")
	switch name {
	case "trim", "uppercase", "lowercase":
		return nil
	case "multiply":
		if !hasArg {
			return fmt.Errorf("%w: multiply requires a numeric argument (e.g. \"multiply:1.2\")", ErrInvalidTransform)
		}
		if _, err := strconv.ParseFloat(arg, 64); err != nil {
			return fmt.Errorf("%w: multiply requires a numeric argument, got %q", ErrInvalidTransform, arg)
		}
		return nil
	case "date_format":
		if !hasArg || arg == "" {
			return fmt.Errorf("%w: date_format requires a layout argument (e.g. \"date_format:YYYY-MM-DD\")", ErrInvalidTransform)
		}
		return nil
	case "static":
		if !hasArg {
			return fmt.Errorf("%w: static requires a value argument (e.g. \"static:TRY\")", ErrInvalidTransform)
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown transform %q", ErrInvalidTransform, name)
	}
}

// toString renders value (a JSON-decoded any: string, float64, bool, or
// nil) as a string - nil becomes "", not "<nil>", so "trim"/"uppercase"
// on a missing field is a harmless no-op rather than a literal "<nil>"
// string.
func toString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// toFloat parses value as a number - accepts a JSON number (float64, the
// json package's own default numeric type) or a numeric string (a value
// that arrived as a string in a partner's own JSON, or was itself
// produced by an earlier "static"/string transform in the same chain).
func toFloat(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("value %q is not numeric", v)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("value %v is not numeric", v)
	}
}

// dateFormatTokenReplacer translates this batch's own design-brief date
// tokens (YYYY/MM/DD/HH/mm/ss) into Go's reference-time layout
// equivalents (2006/01/02/15/04/05) - applied in this specific order so
// e.g. "MM" (month) is replaced before a lone "M" could ever be
// (deliberately not supporting single-letter tokens at all, avoiding
// that ambiguity entirely).
var dateFormatTokenReplacer = strings.NewReplacer(
	"YYYY", "2006", "MM", "01", "DD", "02", "HH", "15", "mm", "04", "ss", "05",
)

// sourceDateLayouts are the input formats formatDate accepts - RFC3339
// (what every JSON API in this codebase itself emits) and a plain
// YYYY-MM-DD date (the other shape a partner system's own JSON commonly
// sends) - tried in order, first successful parse wins.
var sourceDateLayouts = []string{time.RFC3339, "2006-01-02"}

// formatDate parses value as one of sourceDateLayouts, then reformats it
// using layout's own YYYY/MM/DD-token syntax (translated via
// dateFormatTokenReplacer) - see applyOne's own doc comment for the full
// "date_format:LAYOUT" transform contract.
func formatDate(value any, layout string) (string, error) {
	raw := toString(value)
	if raw == "" {
		return "", fmt.Errorf("%w: date_format requires a non-empty date value", ErrInvalidTransform)
	}
	var parsed time.Time
	var err error
	for _, l := range sourceDateLayouts {
		parsed, err = time.Parse(l, raw)
		if err == nil {
			break
		}
	}
	if err != nil {
		return "", fmt.Errorf("%w: date_format: %q is not a recognized date (expected RFC3339 or YYYY-MM-DD)", ErrInvalidTransform, raw)
	}
	goLayout := dateFormatTokenReplacer.Replace(layout)
	return parsed.Format(goLayout), nil
}
