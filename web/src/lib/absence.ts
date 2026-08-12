// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// HR depth batch, frontend side: absence requests (internal/absence.Absence) -
// mirrors lib/hr.ts's own convention.

export type AbsenceType = "vacation" | "sick" | "unpaid" | "maternity" | "other";
export type AbsenceStatus = "pending" | "approved" | "rejected";

export type Absence = {
  id: string;
  personId: string;
  workflowInstanceId?: string;
  requestedBy: string;
  type: AbsenceType;
  startDate: string;
  endDate: string;
  status: AbsenceStatus;
  approvedBy?: string;
  notes?: string;
  createdAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/absences`. */
export async function fetchAbsences(token: string, firmId: string): Promise<Absence[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/absences`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Absence[];
  } catch {
    return null;
  }
}

export type CreateAbsenceInput = {
  personId: string;
  type: AbsenceType;
  startDate: string;
  endDate: string;
  notes?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/absences` (member-gated). */
export async function createAbsence(
  token: string,
  firmId: string,
  input: CreateAbsenceInput,
): Promise<{ ok: true; absence: Absence } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/absences`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, absence: (await res.json()) as Absence };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

async function resolveAbsence(
  token: string,
  firmId: string,
  absenceId: string,
  action: "approve" | "reject",
): Promise<{ ok: true; absence: Absence } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/absences/${encodeURIComponent(absenceId)}/${action}`,
      { method: "PATCH", headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, absence: (await res.json()) as Absence };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `PATCH /api/firms/{firmId}/absences/{id}/approve` (owner-only). */
export function approveAbsence(token: string, firmId: string, absenceId: string) {
  return resolveAbsence(token, firmId, absenceId, "approve");
}

/** Calls the Go backend's `PATCH /api/firms/{firmId}/absences/{id}/reject` (owner-only). */
export function rejectAbsence(token: string, firmId: string, absenceId: string) {
  return resolveAbsence(token, firmId, absenceId, "reject");
}
