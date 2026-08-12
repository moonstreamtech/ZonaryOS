// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Payroll foundation batch, frontend side: payroll periods and entries
// (internal/payroll.Period/Entry) - mirrors lib/hr.ts's own "types +
// fetch helpers, one file per backend package" convention.

export type PeriodStatus = "open" | "processing" | "closed";

export type Period = {
  id: string;
  name: string;
  periodStart: string;
  periodEnd: string;
  status: PeriodStatus;
  createdAt: string;
};

export type Entry = {
  id: string;
  periodId: string;
  personId: string;
  contractId?: string;
  grossAmount: string;
  deductions: Record<string, unknown>;
  netAmount: string;
  currency: string;
  notes?: string;
  createdAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/payroll-periods`. */
export async function fetchPeriods(token: string, firmId: string): Promise<Period[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/payroll-periods`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Period[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/payroll-periods/{id}/entries`. */
export async function fetchEntries(token: string, firmId: string, periodId: string): Promise<Entry[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/payroll-periods/${encodeURIComponent(periodId)}/entries`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Entry[];
  } catch {
    return null;
  }
}

export type CreatePeriodInput = { name: string; periodStart: string; periodEnd: string };

/** Calls the Go backend's `POST /api/firms/{firmId}/payroll-periods` (owner-only). */
export async function createPeriod(
  token: string,
  firmId: string,
  input: CreatePeriodInput,
): Promise<{ ok: true; period: Period } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/payroll-periods`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, period: (await res.json()) as Period };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

export type CreateEntryInput = {
  personId: string;
  grossAmount: string;
  netAmount: string;
  currency?: string;
  notes?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/payroll-periods/{id}/entries` (owner-only). */
export async function createEntry(
  token: string,
  firmId: string,
  periodId: string,
  input: CreateEntryInput,
): Promise<{ ok: true; entry: Entry } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/payroll-periods/${encodeURIComponent(periodId)}/entries`,
      {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify(input),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, entry: (await res.json()) as Entry };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `POST /api/firms/{firmId}/payroll-periods/{id}/close` (owner-only). */
export async function closePeriod(
  token: string,
  firmId: string,
  periodId: string,
): Promise<{ ok: true; period: Period } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/payroll-periods/${encodeURIComponent(periodId)}/close`,
      { method: "POST", headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, period: (await res.json()) as Period };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}
