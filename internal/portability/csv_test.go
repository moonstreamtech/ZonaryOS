// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package portability

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/accounting"
	"github.com/moonstreamtech/ZonaryOS/internal/firm"
)

func sampleExportDocument() ExportDocument {
	firmID := uuid.New()
	return ExportDocument{
		Version:    ExportVersion,
		ExportedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Firm:       firm.Metadata{FirmID: firmID, Name: "Acme Firm"},
		Accounts: []accounting.Account{
			{ID: uuid.New(), FirmID: firmID, Code: "1000", Name: "Cash"},
		},
	}
}

// TestBuildCSV_Structure checks the multi-section shape the design brief
// calls for: each entity is its own section, separated by a blank line
// plus an entity-name header, and every ExportDocument entity is present
// (even ones with zero rows for this sample document) - a firm with no
// data for a given entity still gets that section's name and header row,
// not a silently missing section.
func TestBuildCSV_Structure(t *testing.T) {
	doc := sampleExportDocument()
	out, err := BuildCSV(doc)
	if err != nil {
		t.Fatalf("BuildCSV: %v", err)
	}
	text := string(out)

	expectedSections := []string{
		"Firm", "Accounts", "WorkflowDefinitions", "Rules", "People", "Products",
		"Suppliers", "Customers", "WorkflowInstances", "JournalEntries", "Invoices",
		"Deliveries", "StockLevels",
	}
	for _, name := range expectedSections {
		if !strings.Contains(text, "\n"+name+"\n") && !strings.HasPrefix(text, name+"\n") {
			t.Fatalf("expected section %q to be present, got:\n%s", name, text)
		}
	}

	// Sections are separated by a blank line.
	if !strings.Contains(text, "\n\nAccounts\n") {
		t.Fatalf("expected a blank line before the Accounts section, got:\n%s", text)
	}

	// The Accounts section itself must be valid, parseable CSV with a
	// header row plus the one seeded account row.
	accountsSection := text[strings.Index(text, "Accounts\n")+len("Accounts\n"):]
	accountsSection = accountsSection[:strings.Index(accountsSection, "\n\n")]
	r := csv.NewReader(strings.NewReader(accountsSection))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("Accounts section is not valid CSV: %v", err)
	}
	if len(records) != 2 { // header + one account row
		t.Fatalf("expected 2 CSV records (header + 1 row), got %d: %#v", len(records), records)
	}
	if records[0][0] != "ID" {
		t.Fatalf("expected first Accounts CSV column to be ID, got %q", records[0][0])
	}
}

func TestBuildCSV_EmptySectionStillHasHeader(t *testing.T) {
	doc := sampleExportDocument()
	out, err := BuildCSV(doc)
	if err != nil {
		t.Fatalf("BuildCSV: %v", err)
	}
	text := string(out)
	idx := strings.Index(text, "Invoices\n")
	if idx == -1 {
		t.Fatal("expected an Invoices section")
	}
	rest := text[idx+len("Invoices\n"):]
	end := strings.Index(rest, "\n\n")
	if end == -1 {
		end = len(rest)
	}
	section := strings.TrimRight(rest[:end], "\n")
	if section == "" {
		t.Fatal("expected the empty Invoices section to still have a header row")
	}
}
