// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package portability

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/moonstreamtech/ZonaryOS/internal/firm"
)

// csvSection is one ExportDocument field rendered as its own CSV table -
// name is the section header line handleExport writes before it, items
// must be a slice (or a single value, wrapped by the caller into a
// one-element slice, e.g. Firm) of a struct type.
type csvSection struct {
	name  string
	items any
}

// sectionsForCSV lists ExportDocument's own fields, in the same order
// ExportDocument declares them - CSV export is read-only (no import path,
// see this package's doc comment on scope), so this list only ever needs
// to stay in sync with ExportDocument's fields, not with anything a
// client sends.
func sectionsForCSV(doc ExportDocument) []csvSection {
	return []csvSection{
		{"Firm", []firm.Metadata{doc.Firm}},
		{"Accounts", doc.Accounts},
		{"WorkflowDefinitions", doc.WorkflowDefinitions},
		{"Rules", doc.Rules},
		{"People", doc.People},
		{"Products", doc.Products},
		{"Suppliers", doc.Suppliers},
		{"Customers", doc.Customers},
		{"WorkflowInstances", doc.WorkflowInstances},
		{"JournalEntries", doc.JournalEntries},
		{"Invoices", doc.Invoices},
		{"Deliveries", doc.Deliveries},
		{"StockLevels", doc.StockLevels},
	}
}

// BuildCSV renders doc as a multi-section CSV: each ExportDocument entity
// gets its own section, separated by a blank line and its own entity-name
// header row, using stdlib encoding/csv - no new dependency, matching
// this batch's own "no new dependency" scope boundary. Field names and
// values come entirely from doc's own Go struct definitions via
// reflection, never from external/request input, so there is no
// injection surface here the way a raw SQL query would have (contrast
// internal/reports.BuildQuery, whose entityRegistry allow-list is the
// actual safety boundary for THAT feature, since that data ultimately
// comes from a client-authored query_spec).
func BuildCSV(doc ExportDocument) ([]byte, error) {
	var buf bytes.Buffer
	sections := sectionsForCSV(doc)
	for i, sec := range sections {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(sec.name + "\n")

		header, rows, err := csvRowsFor(sec.items)
		if err != nil {
			return nil, fmt.Errorf("build CSV section %q: %w", sec.name, err)
		}
		w := csv.NewWriter(&buf)
		if len(header) > 0 {
			if err := w.Write(header); err != nil {
				return nil, fmt.Errorf("write CSV header for %q: %w", sec.name, err)
			}
		}
		if err := w.WriteAll(rows); err != nil {
			return nil, fmt.Errorf("write CSV rows for %q: %w", sec.name, err)
		}
		w.Flush()
		if err := w.Error(); err != nil {
			return nil, fmt.Errorf("flush CSV for %q: %w", sec.name, err)
		}
	}
	return buf.Bytes(), nil
}

// csvRowsFor reflects over items (a slice of some struct type) and
// produces a header (the struct's own exported field names, in
// declaration order) plus one row per element (each field rendered via
// csvCellValue). An empty/nil slice still yields a header (from its
// element type) and zero rows - a section that legitimately has no data
// for this firm still shows up with its own name and column headers,
// not silently disappearing.
func csvRowsFor(items any) ([]string, [][]string, error) {
	v := reflect.ValueOf(items)
	if v.Kind() != reflect.Slice {
		return nil, nil, fmt.Errorf("csvRowsFor: expected a slice, got %s", v.Kind())
	}
	elemType := v.Type().Elem()
	if elemType.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("csvRowsFor: expected a slice of structs, got slice of %s", elemType.Kind())
	}

	var header []string
	for i := 0; i < elemType.NumField(); i++ {
		f := elemType.Field(i)
		if !f.IsExported() {
			continue
		}
		header = append(header, f.Name)
	}

	rows := make([][]string, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i)
		var row []string
		for j := 0; j < elemType.NumField(); j++ {
			f := elemType.Field(j)
			if !f.IsExported() {
				continue
			}
			row = append(row, csvCellValue(elem.Field(j)))
		}
		rows = append(rows, row)
	}
	return header, rows, nil
}

var timeType = reflect.TypeOf(time.Time{})

// csvCellValue renders one struct field's value as a single CSV cell -
// nil pointers/maps/slices render as an empty string (never "<nil>"),
// time.Time as RFC3339, and any other composite value (maps, slices,
// nested structs like Invoice.Lines or Customer.CustomFields) as its own
// compact JSON encoding, since CSV itself has no native way to represent
// a nested table inside one cell - the same "flatten to a readable
// scalar, don't lose the data" tradeoff every CSV export tool makes.
func csvCellValue(v reflect.Value) string {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		return csvCellValue(v.Elem())
	}
	if v.Type() == timeType {
		return v.Interface().(time.Time).Format(time.RFC3339)
	}
	switch v.Kind() {
	case reflect.Map, reflect.Slice, reflect.Struct:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return ""
		}
		if v.Kind() == reflect.Map && v.IsNil() {
			return ""
		}
		b, err := json.Marshal(v.Interface())
		if err != nil {
			return ""
		}
		return string(b)
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}
