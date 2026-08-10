// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package reports_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/moonstreamtech/ZonaryOS/internal/reports"
)

func TestBuildQuery_ValidSpec(t *testing.T) {
	firmID := uuid.New()
	spec := reports.QuerySpec{
		Entity:  "invoices",
		GroupBy: "status",
		Metrics: []reports.QueryMetric{
			{Name: "count", Aggregation: "count"},
			{Name: "totalAmount", Aggregation: "sum(total)"},
		},
		Filters: []reports.QueryFilter{
			{Field: "currency", Op: reports.OpEq, Value: "TRY"},
		},
	}

	q, err := reports.BuildQuery(firmID, spec)
	if err != nil {
		t.Fatalf("BuildQuery: %v", err)
	}
	if !strings.Contains(q.SQL, "FROM invoices") {
		t.Fatalf("expected query against invoices table, got %q", q.SQL)
	}
	if !strings.Contains(q.SQL, "GROUP BY status") {
		t.Fatalf("expected GROUP BY status, got %q", q.SQL)
	}
	if !strings.Contains(q.SQL, "firm_id = $1") {
		t.Fatalf("expected firm_id filter, got %q", q.SQL)
	}
	if !strings.Contains(q.SQL, "currency = $2") {
		t.Fatalf("expected currency filter bound as $2, got %q", q.SQL)
	}
	// The filter value must be passed as a bound parameter, never
	// interpolated into the SQL text itself.
	if strings.Contains(q.SQL, "TRY") {
		t.Fatalf("filter value must not appear in SQL text, got %q", q.SQL)
	}
	if len(q.Args) != 2 || q.Args[0] != firmID || q.Args[1] != "TRY" {
		t.Fatalf("unexpected args: %#v", q.Args)
	}
}

func TestBuildQuery_UnknownEntity(t *testing.T) {
	_, err := reports.BuildQuery(uuid.New(), reports.QuerySpec{
		Entity:  "users",
		Metrics: []reports.QueryMetric{{Name: "count", Aggregation: "count"}},
	})
	if err == nil {
		t.Fatal("expected an error for an unknown entity")
	}
	if !strings.Contains(err.Error(), "invalid report query spec") {
		t.Fatalf("expected ErrInvalidQuerySpec, got %v", err)
	}
}

// TestBuildQuery_UnknownFieldRejected is this batch's explicit SQL-
// injection-surface test: a field name that looks like an attempted SQL
// injection must be rejected at the query-spec level (ErrInvalidQuerySpec,
// never reaching a query string) - entityRegistry's own allow-list is
// the actual safety boundary, not string escaping, so a malicious field
// name is indistinguishable from any other unknown field name: both are
// simply not in the map, and both are rejected the same way.
func TestBuildQuery_UnknownFieldRejected(t *testing.T) {
	maliciousFields := []string{
		"id; DROP TABLE invoices; --",
		"id) UNION SELECT password FROM users --",
		"total FROM other_firms_data --",
	}
	for _, field := range maliciousFields {
		t.Run(field, func(t *testing.T) {
			_, err := reports.BuildQuery(uuid.New(), reports.QuerySpec{
				Entity:  "invoices",
				GroupBy: field,
				Metrics: []reports.QueryMetric{{Name: "count", Aggregation: "count"}},
			})
			if err == nil {
				t.Fatalf("expected malicious field %q to be rejected", field)
			}
			if !strings.Contains(err.Error(), "invalid report query spec") {
				t.Fatalf("expected ErrInvalidQuerySpec, got %v", err)
			}

			_, err = reports.BuildQuery(uuid.New(), reports.QuerySpec{
				Entity:  "invoices",
				Metrics: []reports.QueryMetric{{Name: "count", Aggregation: "count"}},
				Filters: []reports.QueryFilter{{Field: field, Op: reports.OpEq, Value: "x"}},
			})
			if err == nil {
				t.Fatalf("expected malicious filter field %q to be rejected", field)
			}
		})
	}
}

func TestBuildQuery_UnsupportedOpForEntity(t *testing.T) {
	// "contains" is only valid on string fields - "total" is numeric.
	_, err := reports.BuildQuery(uuid.New(), reports.QuerySpec{
		Entity:  "invoices",
		Metrics: []reports.QueryMetric{{Name: "count", Aggregation: "count"}},
		Filters: []reports.QueryFilter{{Field: "total", Op: reports.OpContains, Value: "1"}},
	})
	if err == nil {
		t.Fatal("expected an error for contains on a numeric field")
	}
}

func TestBuildQuery_AggregationOnNonNumericFieldRejected(t *testing.T) {
	_, err := reports.BuildQuery(uuid.New(), reports.QuerySpec{
		Entity:  "invoices",
		Metrics: []reports.QueryMetric{{Name: "x", Aggregation: "sum(invoice_number)"}},
	})
	if err == nil {
		t.Fatal("expected an error summing a string field")
	}
}

func TestBuildQuery_DateRange(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	q, err := reports.BuildQuery(uuid.New(), reports.QuerySpec{
		Entity:  "invoices",
		Metrics: []reports.QueryMetric{{Name: "count", Aggregation: "count"}},
		DateRange: &reports.QueryDateRange{
			From: &from, To: &to, Field: "issued_date",
		},
	})
	if err != nil {
		t.Fatalf("BuildQuery: %v", err)
	}
	if !strings.Contains(q.SQL, "issued_date >= $2") || !strings.Contains(q.SQL, "issued_date <= $3") {
		t.Fatalf("expected date range bounds in SQL, got %q", q.SQL)
	}
	if len(q.Args) != 3 || q.Args[1] != from || q.Args[2] != to {
		t.Fatalf("unexpected args: %#v", q.Args)
	}
}

func TestBuildQuery_DateRangeOnNonTimestampFieldRejected(t *testing.T) {
	from := time.Now()
	_, err := reports.BuildQuery(uuid.New(), reports.QuerySpec{
		Entity:  "invoices",
		Metrics: []reports.QueryMetric{{Name: "count", Aggregation: "count"}},
		DateRange: &reports.QueryDateRange{
			From: &from, Field: "total",
		},
	})
	if err == nil {
		t.Fatal("expected an error for a date_range field that isn't a timestamp")
	}
}

func TestBuildQuery_NoMetricsRejected(t *testing.T) {
	_, err := reports.BuildQuery(uuid.New(), reports.QuerySpec{Entity: "invoices"})
	if err == nil {
		t.Fatal("expected an error when no metrics are requested")
	}
}
