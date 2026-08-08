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
