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
