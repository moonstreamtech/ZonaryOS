// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Customer-facing data, frontend side: customers (internal/crm.Customer) -
// mirrors lib/inventory.ts's own "types + fetch helpers, one file per
// backend package" convention.

export type Customer = {
  id: string;
  name: string;
  email?: string;
  phone?: string;
  address?: string;
  taxId?: string;
  creditLimit?: string;
  currency: string;
  customFields: Record<string, unknown>;
  sourceWorkflowInstance?: string;
  createdAt: string;
};

export type InteractionType = "call" | "email" | "meeting" | "note" | "demo" | "proposal";

export type Interaction = {
  id: string;
  customerId: string;
  type: InteractionType;
  date: string;
  summary: string;
  outcome?: string;
  nextAction?: string;
  nextActionDate?: string;
  opportunityId?: string;
  createdBy: string;
  createdAt: string;
};

export type OpportunityStage = "prospect" | "qualified" | "proposal" | "negotiation" | "won" | "lost";

export type Opportunity = {
  id: string;
  customerId: string;
  name: string;
  value?: string;
  currency: string;
  stage: OpportunityStage;
  probability?: number;
  expectedCloseDate?: string;
  assignedToPersonId?: string;
  notes?: string;
  createdAt: string;
  updatedAt: string;
};

export type TimelineEntry = {
  kind: "interaction" | "opportunity";
  date: string;
  interaction?: Interaction;
  opportunity?: Opportunity;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/customers`. */
export async function fetchCustomers(token: string, firmId: string): Promise<Customer[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/customers`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Customer[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/customers/{customerId}`. */
export async function fetchCustomer(token: string, firmId: string, customerId: string): Promise<Customer | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/customers/${encodeURIComponent(customerId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Customer;
  } catch {
    return null;
  }
}

export type CreateCustomerInput = {
  name: string;
  email?: string;
  phone?: string;
  address?: string;
  taxId?: string;
  creditLimit?: string;
  currency?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/customers` (owner-only). */
export async function createCustomer(
  token: string,
  firmId: string,
  input: CreateCustomerInput,
): Promise<{ ok: true; customer: Customer } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/customers`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, customer: (await res.json()) as Customer };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

export type UpdateCustomerInput = Partial<{
  name: string;
  email: string;
  phone: string;
  address: string;
  taxId: string;
  creditLimit: string;
  currency: string;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/customers/{customerId}` (owner-only). */
export async function updateCustomer(
  token: string,
  firmId: string,
  customerId: string,
  patch: UpdateCustomerInput,
): Promise<{ ok: true; customer: Customer } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/customers/${encodeURIComponent(customerId)}`,
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
    return { ok: true, customer: (await res.json()) as Customer };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

// CRM interactions & timeline / opportunities (this batch) - extends
// internal/crm with a per-customer interaction log and a merged
// interaction+opportunity timeline, plus the opportunities pipeline
// itself. Same "types + fetch helpers, inline try/catch" convention as
// the rest of this file (see lib/manufacturing.ts's own doc comment for
// the postJSON-generic-helper alternative this file deliberately does
// not use).

/** Calls the Go backend's `GET /api/firms/{firmId}/customers/{customerId}/interactions`. */
export async function fetchInteractions(
  token: string,
  firmId: string,
  customerId: string,
): Promise<Interaction[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/customers/${encodeURIComponent(customerId)}/interactions`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Interaction[];
  } catch {
    return null;
  }
}

export type CreateInteractionInput = {
  type: InteractionType;
  date: string;
  summary: string;
  outcome?: string;
  nextAction?: string;
  nextActionDate?: string;
  opportunityId?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/customers/{customerId}/interactions` (owner-only). */
export async function createInteraction(
  token: string,
  firmId: string,
  customerId: string,
  input: CreateInteractionInput,
): Promise<{ ok: true; interaction: Interaction } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/customers/${encodeURIComponent(customerId)}/interactions`,
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
    return { ok: true, interaction: (await res.json()) as Interaction };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/customers/{customerId}/timeline`. */
export async function fetchTimeline(
  token: string,
  firmId: string,
  customerId: string,
): Promise<TimelineEntry[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/customers/${encodeURIComponent(customerId)}/timeline`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as TimelineEntry[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/opportunities` (optional `customerId` filter). */
export async function fetchOpportunities(
  token: string,
  firmId: string,
  opts?: { customerId?: string },
): Promise<Opportunity[] | null> {
  try {
    const query = opts?.customerId ? `?customerId=${encodeURIComponent(opts.customerId)}` : "";
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/opportunities${query}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Opportunity[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/opportunities/{opportunityId}`. */
export async function fetchOpportunity(
  token: string,
  firmId: string,
  opportunityId: string,
): Promise<Opportunity | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/opportunities/${encodeURIComponent(opportunityId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Opportunity;
  } catch {
    return null;
  }
}

export type CreateOpportunityInput = {
  customerId: string;
  name: string;
  value?: string;
  currency?: string;
  probability?: number;
  expectedCloseDate?: string;
  assignedToPersonId?: string;
  notes?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/opportunities` (owner-only). */
export async function createOpportunity(
  token: string,
  firmId: string,
  input: CreateOpportunityInput,
): Promise<{ ok: true; opportunity: Opportunity } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/opportunities`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, opportunity: (await res.json()) as Opportunity };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

export type UpdateOpportunityInput = Partial<{
  name: string;
  value: string;
  probability: number;
  expectedCloseDate: string;
  assignedToPersonId: string;
  notes: string;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/opportunities/{opportunityId}` (owner-only). */
export async function updateOpportunity(
  token: string,
  firmId: string,
  opportunityId: string,
  patch: UpdateOpportunityInput,
): Promise<{ ok: true; opportunity: Opportunity } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/opportunities/${encodeURIComponent(opportunityId)}`,
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
    return { ok: true, opportunity: (await res.json()) as Opportunity };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `POST /api/firms/{firmId}/opportunities/{opportunityId}/stage` (owner-only). Terminal stages (won/lost) can't be changed further. */
export async function updateOpportunityStage(
  token: string,
  firmId: string,
  opportunityId: string,
  stage: OpportunityStage,
): Promise<{ ok: true; opportunity: Opportunity } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/opportunities/${encodeURIComponent(opportunityId)}/stage`,
      {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ stage }),
        cache: "no-store",
      },
    );
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, opportunity: (await res.json()) as Opportunity };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}
