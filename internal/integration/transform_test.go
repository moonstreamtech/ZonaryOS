// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package integration

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestApplyOne_EachTransformType covers Part 4's own full transform list -
// pure unit tests, no DB needed (applyOne/ApplyTransforms take/return
// plain Go values only).
func TestApplyOne_EachTransformType(t *testing.T) {
	cases := []struct {
		name  string
		value any
		spec  string
		want  any
	}{
		{"trim", "  hello  ", "trim", "hello"},
		{"uppercase", "hello", "uppercase", "HELLO"},
		{"lowercase", "HELLO", "lowercase", "hello"},
		{"multiply float", 10.0, "multiply:1.2", 12.0},
		{"multiply numeric string", "10", "multiply:1.2", 12.0},
		{"date_format from RFC3339", "2026-08-18T10:00:00Z", "date_format:YYYY-MM-DD", "2026-08-18"},
		{"date_format from plain date", "2026-08-18", "date_format:YYYY/MM/DD", "2026/08/18"},
		{"static ignores value", "anything", "static:TRY", "TRY"},
		{"static ignores nil value", nil, "static:TRY", "TRY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyOne(tc.value, tc.spec)
			if err != nil {
				t.Fatalf("applyOne(%v, %q): unexpected error: %v", tc.value, tc.spec, err)
			}
			if got != tc.want {
				t.Fatalf("applyOne(%v, %q) = %v, want %v", tc.value, tc.spec, got, tc.want)
			}
		})
	}
}

// TestApplyTransforms_ChainsInOrder is the "chaining" requirement:
// ["trim","uppercase"] must run uppercase on trim's own output, not both
// independently on the original value.
func TestApplyTransforms_ChainsInOrder(t *testing.T) {
	got, err := ApplyTransforms("  hello  ", TransformChain{"trim", "uppercase"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "HELLO" {
		t.Fatalf("got %v, want %q", got, "HELLO")
	}
}

// TestApplyOne_UnknownTransformReturnsError is the "unknown transform
// returns error" requirement.
func TestApplyOne_UnknownTransformReturnsError(t *testing.T) {
	_, err := applyOne("value", "not_a_real_transform")
	if !errors.Is(err, ErrInvalidTransform) {
		t.Fatalf("expected ErrInvalidTransform, got %v", err)
	}
}

// TestValidateTransformSpec covers validateTransformSpec's own syntax-only
// validation, used at mapping-save time.
func TestValidateTransformSpec(t *testing.T) {
	valid := []string{"trim", "uppercase", "lowercase", "multiply:1.2", "date_format:YYYY-MM-DD", "static:TRY"}
	for _, spec := range valid {
		if err := validateTransformSpec(spec); err != nil {
			t.Errorf("validateTransformSpec(%q): unexpected error: %v", spec, err)
		}
	}
	invalid := []string{"multiply", "multiply:notanumber", "date_format", "static", "bogus"}
	for _, spec := range invalid {
		if err := validateTransformSpec(spec); !errors.Is(err, ErrInvalidTransform) {
			t.Errorf("validateTransformSpec(%q): expected ErrInvalidTransform, got %v", spec, err)
		}
	}
}

// TestApplyFieldMappings_AppliesTransformsPerField exercises
// ApplyFieldMappings end to end: source values read by SourceField,
// transformed, and written under TargetField.
func TestApplyFieldMappings_AppliesTransformsPerField(t *testing.T) {
	source := map[string]any{
		"name":  "  widget  ",
		"price": 10.0,
	}
	mappings := []FieldMapping{
		{SourceField: "name", TargetField: "productName", Transform: TransformChain{"trim", "uppercase"}},
		{SourceField: "price", TargetField: "listPrice", Transform: TransformChain{"multiply:1.2"}},
		{SourceField: "currency", TargetField: "currency", Transform: TransformChain{"static:TRY"}},
	}
	got, err := ApplyFieldMappings(source, mappings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["productName"] != "WIDGET" {
		t.Errorf("productName = %v, want WIDGET", got["productName"])
	}
	if got["listPrice"] != 12.0 {
		t.Errorf("listPrice = %v, want 12.0", got["listPrice"])
	}
	if got["currency"] != "TRY" {
		t.Errorf("currency = %v, want TRY", got["currency"])
	}
}

// TestTransformChain_JSONRoundTrip covers TransformChain's own custom
// (Un)MarshalJSON: a single string stays a single string, an array stays
// an array.
func TestTransformChain_JSONRoundTrip(t *testing.T) {
	var single FieldMapping
	if err := json.Unmarshal([]byte(`{"source_field":"a","target_field":"b","transform":"uppercase"}`), &single); err != nil {
		t.Fatalf("unmarshal single: %v", err)
	}
	if len(single.Transform) != 1 || single.Transform[0] != "uppercase" {
		t.Fatalf("expected single-element chain [\"uppercase\"], got %v", single.Transform)
	}

	var multi FieldMapping
	if err := json.Unmarshal([]byte(`{"source_field":"a","target_field":"b","transform":["trim","uppercase"]}`), &multi); err != nil {
		t.Fatalf("unmarshal multiple: %v", err)
	}
	if len(multi.Transform) != 2 || multi.Transform[0] != "trim" || multi.Transform[1] != "uppercase" {
		t.Fatalf("expected chain [\"trim\",\"uppercase\"], got %v", multi.Transform)
	}
}
