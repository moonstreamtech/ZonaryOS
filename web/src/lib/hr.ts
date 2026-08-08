// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Vision §3's minimal HR foundation, frontend side: people and their
// contracts (internal/hr.Person/Contract) - mirrors lib/accounting.ts's
// own "types + fetch helpers, one file per backend package" convention.

export type PersonType = "employee" | "contractor" | "partner";
export type PersonStatus = "active" | "inactive";

export type Person = {
  id: string;
  fullName: string;
  type: PersonType;
  email?: string;
  phone?: string;
  startDate?: string;
  endDate?: string;
  status: PersonStatus;
  customFields: Record<string, unknown>;
  createdAt: string;
};

export type Contract = {
  id: string;
  personId: string;
  type: string;
  amount?: string;
  currency: string;
  startDate: string;
  endDate?: string;
  notes?: string;
  createdAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/people` - the HR
 * page's roster data source. Optional `status` filters to
 * "active"/"inactive". Member-gated on the backend; returns null on any
 * failure, same swallow-to-null convention as lib/accounting.ts's own
 * read helpers.
 */
export async function fetchPeople(
  token: string,
  firmId: string,
  opts: { status?: PersonStatus } = {},
): Promise<Person[] | null> {
  try {
    const params = new URLSearchParams();
    if (opts.status) params.set("status", opts.status);
    const qs = params.toString();
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/people${qs ? `?${qs}` : ""}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Person[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/people/{personId}`. */
export async function fetchPerson(token: string, firmId: string, personId: string): Promise<Person | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/people/${encodeURIComponent(personId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Person;
  } catch {
    return null;
  }
}

export type CreatePersonInput = {
  fullName: string;
  type: PersonType;
  email?: string;
  phone?: string;
  startDate?: string;
  endDate?: string;
};

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/people` (owner-only).
 * Failures are surfaced to the caller, same convention as
 * lib/accounting.ts's createAccount.
 */
export async function createPerson(
  token: string,
  firmId: string,
  input: CreatePersonInput,
): Promise<{ ok: true; person: Person } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/people`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, person: (await res.json()) as Person };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

export type UpdatePersonInput = Partial<{
  fullName: string;
  email: string;
  phone: string;
  startDate: string;
  endDate: string;
  status: PersonStatus;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/people/{personId}` (owner-only). */
export async function updatePerson(
  token: string,
  firmId: string,
  personId: string,
  patch: UpdatePersonInput,
): Promise<{ ok: true; person: Person } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/people/${encodeURIComponent(personId)}`,
      {
        method: "PATCH",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify(patch),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, person: (await res.json()) as Person };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/people/{personId}/contracts`. */
export async function fetchContracts(token: string, firmId: string, personId: string): Promise<Contract[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/people/${encodeURIComponent(personId)}/contracts`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Contract[];
  } catch {
    return null;
  }
}

export type CreateContractInput = {
  type: string;
  amount?: string;
  currency?: string;
  startDate: string;
  endDate?: string;
  notes?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/people/{personId}/contracts` (owner-only). */
export async function createContract(
  token: string,
  firmId: string,
  personId: string,
  input: CreateContractInput,
): Promise<{ ok: true; contract: Contract } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/people/${encodeURIComponent(personId)}/contracts`,
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
    return { ok: true, contract: (await res.json()) as Contract };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}
