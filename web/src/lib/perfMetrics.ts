// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Performance monitoring's read-only metrics summary (this batch), frontend
// side - mirrors lib/platformAdmin.ts's own "returns null on any failure"
// convention: a non-2xx response (including the 404 a non-allowlisted
// caller gets from internal/platformadmin.Allowlist) is not distinguished
// from any other failure here.

export type EndpointMetric = {
  pathPattern: string;
  p50Ms: number;
  p95Ms: number;
  p99Ms: number;
  errorRate: number;
  requestCount: number;
};

export type VolumeBucket = {
  hour: string;
  count: number;
};

export type MetricsSummary = {
  endpoints: EndpointMetric[];
  topSlowest: EndpointMetric[];
  volumeByHour: VolumeBucket[];
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/platform-admin/metrics/summary`
 * (internal/platformadmin.Allowlist-gated). Fixed last-24h window, no
 * query params - same shape the backend already returns.
 */
export async function fetchMetricsSummary(token: string): Promise<MetricsSummary | null> {
  try {
    const res = await fetch(`${apiBase()}/api/platform-admin/metrics/summary`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as MetricsSummary;
  } catch {
    return null;
  }
}
