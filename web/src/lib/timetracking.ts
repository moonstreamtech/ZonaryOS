// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Time tracking batch, frontend side: time entries
// (internal/timetracking.Entry) - mirrors lib/hr.ts's own convention.

export type TimeEntry = {
  id: string;
  personId: string;
  workflowInstanceId?: string;
  createdBy: string;
  date: string;
  hours: string;
  description?: string;
  billable: boolean;
  createdAt: string;
};

export type PersonTotal = { personId: string; totalHours: string };

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

export type FetchEntriesOptions = { personId?: string; from?: string; to?: string };

/** Calls the Go backend's `GET /api/firms/{firmId}/time-entries`. */
export async function fetchTimeEntries(
  token: string,
  firmId: string,
  opts: FetchEntriesOptions = {},
): Promise<TimeEntry[] | null> {
  try {
    const params = new URLSearchParams();
    if (opts.personId) params.set("person_id", opts.personId);
    if (opts.from) params.set("from", opts.from);
    if (opts.to) params.set("to", opts.to);
    const qs = params.toString();
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/time-entries${qs ? `?${qs}` : ""}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as TimeEntry[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/time-entries/summary`. */
export async function fetchTimeSummary(
  token: string,
  firmId: string,
  opts: FetchEntriesOptions = {},
): Promise<PersonTotal[] | null> {
  try {
    const params = new URLSearchParams();
    if (opts.personId) params.set("person_id", opts.personId);
    if (opts.from) params.set("from", opts.from);
    if (opts.to) params.set("to", opts.to);
    const qs = params.toString();
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/time-entries/summary${qs ? `?${qs}` : ""}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as PersonTotal[];
  } catch {
    return null;
  }
}

export type CreateTimeEntryInput = {
  personId: string;
  date: string;
  hours: string;
  description?: string;
  billable?: boolean;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/time-entries` (member-gated). */
export async function createTimeEntry(
  token: string,
  firmId: string,
  input: CreateTimeEntryInput,
): Promise<{ ok: true; entry: TimeEntry } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/time-entries`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, entry: (await res.json()) as TimeEntry };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}
