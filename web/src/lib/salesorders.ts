// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Sales orders, frontend side (internal/salesorders) - mirrors
// lib/invoicing.ts's own "types + fetch helpers, one file per backend
// package" convention.

export type SalesOrderStatus = "draft" | "confirmed" | "picking" | "shipped" | "delivered" | "cancelled";

export type SalesOrderLine = {
  id: string;
  productId?: string;
  description: string;
  quantity: string;
  unitPrice: string;
  taxRate: string;
  lineTotal: string;
};

export type SalesOrder = {
  id: string;
  orderNumber: string;
  customerId?: string;
  status: SalesOrderStatus;
  shippingAddress?: string;
  notes?: string;
  currency: string;
  subtotal: string;
  taxAmount: string;
  total: string;
  sourceWorkflowInstance?: string;
  createdAt: string;
  lines?: SalesOrderLine[];
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/sales-orders`. */
export async function fetchSalesOrders(
  token: string,
  firmId: string,
  opts?: { status?: SalesOrderStatus },
): Promise<SalesOrder[] | null> {
  try {
    const query = opts?.status ? `?status=${encodeURIComponent(opts.status)}` : "";
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/sales-orders${query}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as SalesOrder[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/sales-orders/{orderId}` (with lines). */
export async function fetchSalesOrder(token: string, firmId: string, orderId: string): Promise<SalesOrder | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/sales-orders/${encodeURIComponent(orderId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as SalesOrder;
  } catch {
    return null;
  }
}

export type SalesOrderLineInput = {
  productId?: string;
  description: string;
  quantity: string;
  unitPrice: string;
  taxRate?: string;
};

export type CreateSalesOrderInput = {
  customerId?: string;
  shippingAddress?: string;
  notes?: string;
  currency?: string;
  lines: SalesOrderLineInput[];
};

type ApiResult<T extends string, V> = { ok: true } & Record<T, V> | { ok: false; error: string; status: number };

async function postJSON<T extends string, V>(
  url: string,
  method: "POST" | "PATCH",
  token: string,
  key: T,
  body?: unknown,
): Promise<ApiResult<T, V>> {
  try {
    const res = await fetch(url, {
      method,
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    const value = (await res.json()) as V;
    return { ok: true, [key]: value } as ApiResult<T, V>;
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `POST /api/firms/{firmId}/sales-orders` (owner-only). */
export async function createSalesOrder(
  token: string,
  firmId: string,
  input: CreateSalesOrderInput,
): Promise<ApiResult<"salesOrder", SalesOrder>> {
  return postJSON(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/sales-orders`, "POST", token, "salesOrder", input);
}

/** Calls the Go backend's `PATCH /api/firms/{firmId}/sales-orders/{orderId}` (owner-only). */
export async function updateSalesOrderStatus(
  token: string,
  firmId: string,
  orderId: string,
  status: SalesOrderStatus,
): Promise<ApiResult<"salesOrder", SalesOrder>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/sales-orders/${encodeURIComponent(orderId)}`,
    "PATCH",
    token,
    "salesOrder",
    { status },
  );
}
