// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apifields

import (
	"encoding/json"
	"errors"
	"testing"
)

type sampleResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	SKU  string `json:"sku"`
}

var sampleAllowed = map[string]bool{"id": true, "name": true, "sku": true}

func TestParseFields(t *testing.T) {
	if got := ParseFields(""); got != nil {
		t.Fatalf("expected nil for empty raw, got %v", got)
	}
	got := ParseFields(" id, name ,sku,,")
	want := []string{"id", "name", "sku"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestProject_ReturnsOnlyRequestedFields is the "?fields=id,name returns
// only those fields" requirement.
func TestProject_ReturnsOnlyRequestedFields(t *testing.T) {
	items := []sampleResponse{
		{ID: "1", Name: "Widget", SKU: "SKU-1"},
		{ID: "2", Name: "Gadget", SKU: "SKU-2"},
	}
	raw, err := Project(items, ParseFields("id,name"), sampleAllowed)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal projected result: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 items, got %d", len(decoded))
	}
	for _, item := range decoded {
		if len(item) != 2 {
			t.Fatalf("expected exactly 2 fields per item, got %v", item)
		}
		if _, ok := item["id"]; !ok {
			t.Fatalf("expected \"id\" field present, got %v", item)
		}
		if _, ok := item["name"]; !ok {
			t.Fatalf("expected \"name\" field present, got %v", item)
		}
		if _, ok := item["sku"]; ok {
			t.Fatalf("expected \"sku\" field ABSENT from a projected result, got %v", item)
		}
	}
}

// TestProject_UnknownFieldRejected is the "unknown field names are
// rejected (400)" requirement - the caller maps ErrUnknownField to 400.
func TestProject_UnknownFieldRejected(t *testing.T) {
	items := []sampleResponse{{ID: "1", Name: "Widget", SKU: "SKU-1"}}
	_, err := Project(items, ParseFields("id,bogusField"), sampleAllowed)
	if !errors.Is(err, ErrUnknownField) {
		t.Fatalf("expected ErrUnknownField, got %v", err)
	}
}

// TestProject_NoFieldsReturnsFullResponse covers the "no ?fields=" case -
// every field, unfiltered.
func TestProject_NoFieldsReturnsFullResponse(t *testing.T) {
	items := []sampleResponse{{ID: "1", Name: "Widget", SKU: "SKU-1"}}
	raw, err := Project(items, nil, sampleAllowed)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded[0]) != 3 {
		t.Fatalf("expected all 3 fields present, got %v", decoded[0])
	}
}

func TestParseUpdatedSince(t *testing.T) {
	if got, err := ParseUpdatedSince(""); err != nil || got != nil {
		t.Fatalf("expected (nil, nil) for empty raw, got (%v, %v)", got, err)
	}
	got, err := ParseUpdatedSince("2026-08-01T00:00:00Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.Year() != 2026 || got.Month() != 8 || got.Day() != 1 {
		t.Fatalf("unexpected parsed time: %v", got)
	}
	if _, err := ParseUpdatedSince("not-a-timestamp"); err == nil {
		t.Fatal("expected an error for a malformed updated_since value")
	}
}
