// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Analytics/BI/advanced reporting batch, Part 4, frontend side
// (internal/scheduledreports) - mirrors lib/webhooks.ts's own
// "types + fetch helpers, one file per backend package" convention;
// owner-gated CRUD, same tier as webhooks.

export type ScheduleInterval = "daily" | "weekly" | "monthly";

export type ScheduledReport = {
  id: string;
  definitionId: string;
  name: string;
  scheduleInterval: ScheduleInterval;
  lastRunAt?: string;
  nextRunAt?: string;
  recipientUserIds: string[];
  isActive: boolean;
  createdAt: string;
};

export type ScheduledReportInput = {
  definitionId: string;
  name: string;
  scheduleInterval: ScheduleInterval;
  recipientUserIds: string[];
  isActive: boolean;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/scheduled-reports` (owner-only). */
export async function fetchScheduledReports(token: string, firmId: string): Promise<ScheduledReport[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/scheduled-reports`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as ScheduledReport[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `POST /api/firms/{firmId}/scheduled-reports` (owner-only). */
export async function createScheduledReport(
  token: string,
  firmId: string,
  input: ScheduledReportInput,
): Promise<{ ok: true; scheduled: ScheduledReport } | { ok: false; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/scheduled-reports`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) return { ok: false, status: res.status };
    return { ok: true, scheduled: (await res.json()) as ScheduledReport };
  } catch {
    return { ok: false, status: 0 };
  }
}

/** Calls the Go backend's `PATCH /api/firms/{firmId}/scheduled-reports/{id}` (owner-only). */
export async function updateScheduledReport(
  token: string,
  firmId: string,
  id: string,
  input: ScheduledReportInput,
): Promise<{ ok: true; scheduled: ScheduledReport } | { ok: false; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/scheduled-reports/${encodeURIComponent(id)}`,
      {
        method: "PATCH",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify(input),
        cache: "no-store",
      },
    );
    if (!res.ok) return { ok: false, status: res.status };
    return { ok: true, scheduled: (await res.json()) as ScheduledReport };
  } catch {
    return { ok: false, status: 0 };
  }
}

/** Calls the Go backend's `DELETE /api/firms/{firmId}/scheduled-reports/{id}` (owner-only). */
export async function deleteScheduledReport(token: string, firmId: string, id: string): Promise<boolean> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/scheduled-reports/${encodeURIComponent(id)}`,
      { method: "DELETE", headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    return res.ok;
  } catch {
    return false;
  }
}
