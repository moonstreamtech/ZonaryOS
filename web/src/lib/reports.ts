// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Vision §3's reporting foundation, frontend side: the KPI dashboard
// (internal/reports.KPIResult) - mirrors lib/accounting.ts's own
// "types + fetch helpers" convention.

export type KPIUnit = "currency" | "count";

export type KPIResult = {
  key: string;
  unit: KPIUnit;
  value: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/reports/kpis` - the
 * /reports page's data source. Member-gated on the backend. Returns null
 * on failure, same swallow-to-null convention as lib/accounting.ts's own
 * read helpers.
 */
export async function fetchDashboardKPIs(token: string, firmId: string): Promise<KPIResult[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/reports/kpis`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as KPIResult[];
  } catch {
    return null;
  }
}

// Parametric reporting engine (this batch) - report_definitions/
// saved_report_runs, internal/reports.QuerySpec's own frontend mirror.
// Filters/metrics/date_range shapes match internal/reports.QuerySpec's
// own JSON tags exactly (snake_case group_by/date_range - the one
// deliberate exception to this codebase's usual camelCase wire format,
// since this is a structured *query* descriptor, not a REST resource
// shape).

export type CompareOp = "eq" | "neq" | "lt" | "gt" | "lte" | "gte" | "contains";

export type QueryFilter = {
  field: string;
  op: CompareOp;
  value: string | number | boolean;
};

export type QueryMetric = {
  name: string;
  aggregation: string; // "count" | "sum(field)" | "avg(field)" | "min(field)" | "max(field)"
};

export type QueryDateRange = {
  from?: string;
  to?: string;
  field: string;
};

export type QuerySpec = {
  entity: string;
  filters?: QueryFilter[];
  group_by?: string;
  metrics: QueryMetric[];
  date_range?: QueryDateRange;
};

export type ReportDefinition = {
  id: string;
  name: string;
  description?: string;
  querySpec: QuerySpec;
  createdBy: string;
  createdAt: string;
};

export type ReportRow = {
  groupKey?: string;
  metrics: Record<string, unknown>;
};

export async function fetchReportDefinitions(token: string, firmId: string): Promise<ReportDefinition[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/report-definitions`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as ReportDefinition[];
  } catch {
    return null;
  }
}

export type ReportDefinitionInput = {
  name: string;
  description?: string;
  querySpec: QuerySpec;
};

export async function createReportDefinition(
  token: string,
  firmId: string,
  input: ReportDefinitionInput,
): Promise<{ ok: true; definition: ReportDefinition } | { ok: false; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/report-definitions`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) return { ok: false, status: res.status };
    return { ok: true, definition: (await res.json()) as ReportDefinition };
  } catch {
    return { ok: false, status: 0 };
  }
}

// Part 3 of the analytics/BI/advanced reporting batch: cohort analysis
// (internal/reports/cohort.go). Only one (entity, cohort_by, metric)
// combination is implemented server-side today - see that file's own
// doc comment - this type mirrors CohortTable/CohortRow's own JSON shape
// exactly. `values[i]` is `null` at a period with no data yet, a real
// distinct "no data" signal, not zero (see internal/reports/cohort.go's
// own CohortRow.Values doc comment) - callers must render that
// distinctly, never as "0".
export type CohortRow = {
  cohortMonth: string;
  cohortSize: number;
  values: (number | null)[];
};

export type CohortTable = {
  periods: number;
  cohorts: CohortRow[];
};

export type CohortSpecInput = {
  entity: string;
  cohortBy: string;
  metric: string;
  periods?: number;
};

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/reports/cohorts` -
 * the /reports "Cohort Analysis" tab's own data source. Member-gated.
 * Returns null on failure, same swallow-to-null convention as
 * fetchDashboardKPIs.
 */
export async function fetchCohortAnalysis(
  token: string,
  firmId: string,
  spec: CohortSpecInput,
): Promise<CohortTable | null> {
  try {
    const url = new URL(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/reports/cohorts`);
    url.searchParams.set("entity", spec.entity);
    url.searchParams.set("cohort_by", spec.cohortBy);
    url.searchParams.set("metric", spec.metric);
    if (spec.periods) url.searchParams.set("periods", String(spec.periods));
    const res = await fetch(url, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as CohortTable;
  } catch {
    return null;
  }
}

export async function runReport(
  token: string,
  firmId: string,
  definitionId: string,
): Promise<{ ok: true; rows: ReportRow[] } | { ok: false; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/report-definitions/${encodeURIComponent(definitionId)}/run`,
      { method: "POST", headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return { ok: false, status: res.status };
    return { ok: true, rows: (await res.json()) as ReportRow[] };
  } catch {
    return { ok: false, status: 0 };
  }
}
