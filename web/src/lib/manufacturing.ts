// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Manufacturing module foundation, frontend side (internal/manufacturing)
// - mirrors lib/salesorders.ts's own "types + fetch helpers, one file per
// backend package" convention.

export type BOMLine = {
  id: string;
  componentProductId: string;
  quantityPerUnit: string;
  unit: string;
  notes?: string;
};

export type BOM = {
  id: string;
  productId: string;
  name: string;
  version: string;
  isActive: boolean;
  unitYield: string;
  notes?: string;
  createdAt: string;
  lines?: BOMLine[];
};

export type ProductionOrderStatus = "planned" | "in_progress" | "completed" | "cancelled";

export type MaterialIssue = {
  id: string;
  productId: string;
  quantityIssued: string;
  issuedAt: string;
};

export type ProductionOrder = {
  id: string;
  orderNumber: string;
  productId: string;
  bomId: string;
  quantityPlanned: string;
  quantityProduced: string;
  status: ProductionOrderStatus;
  plannedStart?: string;
  plannedEnd?: string;
  actualStart?: string;
  actualEnd?: string;
  notes?: string;
  createdAt: string;
  materialIssues?: MaterialIssue[];
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/boms`. */
export async function fetchBOMs(token: string, firmId: string, opts?: { productId?: string }): Promise<BOM[] | null> {
  try {
    const query = opts?.productId ? `?productId=${encodeURIComponent(opts.productId)}` : "";
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/boms${query}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as BOM[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/boms/{bomId}` (with lines). */
export async function fetchBOM(token: string, firmId: string, bomId: string): Promise<BOM | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/boms/${encodeURIComponent(bomId)}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as BOM;
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/production-orders`. */
export async function fetchProductionOrders(
  token: string,
  firmId: string,
  opts?: { status?: ProductionOrderStatus },
): Promise<ProductionOrder[] | null> {
  try {
    const query = opts?.status ? `?status=${encodeURIComponent(opts.status)}` : "";
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/production-orders${query}`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as ProductionOrder[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/production-orders/{orderId}` (with material issues). */
export async function fetchProductionOrder(token: string, firmId: string, orderId: string): Promise<ProductionOrder | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/production-orders/${encodeURIComponent(orderId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as ProductionOrder;
  } catch {
    return null;
  }
}

export type BOMLineInput = {
  componentProductId: string;
  quantityPerUnit: string;
  unit?: string;
  notes?: string;
};

export type CreateBOMInput = {
  productId: string;
  name: string;
  version?: string;
  isActive?: boolean;
  unitYield?: string;
  notes?: string;
  lines: BOMLineInput[];
};

type ApiResult<T extends string, V> = { ok: true } & Record<T, V> | { ok: false; error: string; status: number };

async function postJSON<T extends string, V>(
  url: string,
  method: "POST" | "PATCH" | "DELETE",
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
    const value = res.status === 204 ? (undefined as V) : ((await res.json()) as V);
    return { ok: true, [key]: value } as ApiResult<T, V>;
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `POST /api/firms/{firmId}/boms` (owner-only). */
export async function createBOM(token: string, firmId: string, input: CreateBOMInput): Promise<ApiResult<"bom", BOM>> {
  return postJSON(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/boms`, "POST", token, "bom", input);
}

/** Calls the Go backend's `PATCH /api/firms/{firmId}/boms/{bomId}` (owner-only). */
export async function updateBOM(
  token: string,
  firmId: string,
  bomId: string,
  input: { name?: string; notes?: string; isActive?: boolean },
): Promise<ApiResult<"bom", BOM>> {
  return postJSON(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/boms/${encodeURIComponent(bomId)}`, "PATCH", token, "bom", input);
}

export type CreateProductionOrderInput = {
  productId: string;
  bomId: string;
  quantityPlanned: string;
  plannedStart?: string;
  plannedEnd?: string;
  notes?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/production-orders` (owner-only). */
export async function createProductionOrder(
  token: string,
  firmId: string,
  input: CreateProductionOrderInput,
): Promise<ApiResult<"productionOrder", ProductionOrder>> {
  return postJSON(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/production-orders`, "POST", token, "productionOrder", input);
}

/** Calls the Go backend's `POST /api/firms/{firmId}/production-orders/{orderId}/start` (owner-only). */
export async function startProductionOrder(
  token: string,
  firmId: string,
  orderId: string,
): Promise<ApiResult<"productionOrder", ProductionOrder>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/production-orders/${encodeURIComponent(orderId)}/start`,
    "POST",
    token,
    "productionOrder",
  );
}

/** Calls the Go backend's `POST /api/firms/{firmId}/production-orders/{orderId}/complete` (owner-only). */
export async function completeProductionOrder(
  token: string,
  firmId: string,
  orderId: string,
  quantityProduced: string,
): Promise<ApiResult<"productionOrder", ProductionOrder>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/production-orders/${encodeURIComponent(orderId)}/complete`,
    "POST",
    token,
    "productionOrder",
    { quantityProduced },
  );
}

/** Calls the Go backend's `POST /api/firms/{firmId}/production-orders/{orderId}/cancel` (owner-only). */
export async function cancelProductionOrder(
  token: string,
  firmId: string,
  orderId: string,
): Promise<ApiResult<"productionOrder", ProductionOrder>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/production-orders/${encodeURIComponent(orderId)}/cancel`,
    "POST",
    token,
    "productionOrder",
  );
}
