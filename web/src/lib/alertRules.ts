// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Performance monitoring's alert-rule CRUD (this batch), frontend side -
// mirrors lib/consolidation.ts's own "types + fetch helpers, one file per
// backend concern, generic error passthrough for writes" convention.
// Every /alert-rules endpoint is internal/platformadmin.Allowlist-gated,
// same posture as lib/platformAdmin.ts's own fetchPlatformAdminFirms: a
// non-2xx response (including the 404 a non-allowlisted caller gets) is
// not distinguished from any other failure for the read helper, and
// surfaced as a generic { ok: false, status } for the write helpers.

export type AlertMetric = "error_rate" | "response_time_p95" | "volume_drop";

export type AlertRule = {
  id: string;
  name: string;
  metric: AlertMetric;
  threshold: number;
  windowMinutes: number;
  isActive: boolean;
  createdAt: string;
};

export type AlertRuleInput = {
  name: string;
  metric: AlertMetric;
  threshold: number;
  windowMinutes: number;
  isActive: boolean;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/platform-admin/alert-rules`
 * (internal/platformadmin.Allowlist-gated).
 */
export async function fetchAlertRules(token: string): Promise<AlertRule[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/platform-admin/alert-rules`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as AlertRule[];
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `POST /api/platform-admin/alert-rules`. A 400
 * (invalid metric/empty name/non-positive windowMinutes) is passed
 * through as `{ ok: false, status: 400 }`, same convention every other
 * platform-admin write helper uses.
 */
export async function createAlertRule(
  token: string,
  input: AlertRuleInput,
): Promise<{ ok: true; rule: AlertRule } | { ok: false; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/platform-admin/alert-rules`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      return { ok: false, status: res.status };
    }
    return { ok: true, rule: (await res.json()) as AlertRule };
  } catch {
    return { ok: false, status: 0 };
  }
}

/**
 * Calls the Go backend's `PATCH /api/platform-admin/alert-rules/{ruleId}`.
 * 404 if ruleId doesn't exist, passed through the same way.
 */
export async function updateAlertRule(
  token: string,
  ruleId: string,
  input: AlertRuleInput,
): Promise<{ ok: true; rule: AlertRule } | { ok: false; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/platform-admin/alert-rules/${ruleId}`, {
      method: "PATCH",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      return { ok: false, status: res.status };
    }
    return { ok: true, rule: (await res.json()) as AlertRule };
  } catch {
    return { ok: false, status: 0 };
  }
}

/**
 * Calls the Go backend's `DELETE /api/platform-admin/alert-rules/{ruleId}`.
 * 204 on success, 404 if not found (passed through as `{ ok: false }`).
 */
export async function deleteAlertRule(
  token: string,
  ruleId: string,
): Promise<{ ok: true } | { ok: false; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/platform-admin/alert-rules/${ruleId}`, {
      method: "DELETE",
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) {
      return { ok: false, status: res.status };
    }
    return { ok: true };
  } catch {
    return { ok: false, status: 0 };
  }
}
