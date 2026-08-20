// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Performance monitoring's alert-event feed (this batch), frontend side -
// mirrors lib/platformAdmin.ts's own "returns null on any failure"
// convention. This batch never auto-resolves an alert event, so every row
// returned today is effectively "open" - resolvedAt is modeled as
// optional/null purely to match the backend's own JSON shape, not because
// this frontend has any resolve action to offer yet.

export type AlertEvent = {
  id: string;
  ruleId: string;
  ruleName: string;
  triggeredAt: string;
  metricValue: number;
  resolvedAt?: string | null;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/platform-admin/alerts`
 * (internal/platformadmin.Allowlist-gated) - up to 50 recent alert
 * events, most recent first.
 */
export async function fetchAlerts(token: string): Promise<AlertEvent[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/platform-admin/alerts`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as AlertEvent[];
  } catch {
    return null;
  }
}
