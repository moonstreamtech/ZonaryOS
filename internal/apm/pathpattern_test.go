// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package apm_test

import (
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/apm"
)

func TestNormalizePathPattern_ReplacesUUIDSegmentsWithPlaceholder(t *testing.T) {
	got := apm.NormalizePathPattern("/api/firms/3aa7de0c-a5a2-8181-ac86-ef800f628f5d/products/2b1e9a10-1234-4abc-9def-000000000001")
	want := "/api/firms/{id}/products/{id}"
	if got != want {
		t.Errorf("NormalizePathPattern() = %q, want %q", got, want)
	}
}

func TestNormalizePathPattern_LeavesNonUUIDPathUnchanged(t *testing.T) {
	got := apm.NormalizePathPattern("/api/version")
	if got != "/api/version" {
		t.Errorf("NormalizePathPattern() = %q, want unchanged", got)
	}
}

func TestExtractFirmID_FindsUUIDAfterFirmsSegment(t *testing.T) {
	firmID := "3aa7de0c-a5a2-8181-ac86-ef800f628f5d"
	got := apm.ExtractFirmID("/api/firms/" + firmID + "/products")
	if got == nil {
		t.Fatal("expected a non-nil firm ID")
	}
	if got.String() != firmID {
		t.Errorf("ExtractFirmID() = %q, want %q", got.String(), firmID)
	}
}

func TestExtractFirmID_NilWhenPathHasNoFirmsSegment(t *testing.T) {
	if got := apm.ExtractFirmID("/api/version"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}
