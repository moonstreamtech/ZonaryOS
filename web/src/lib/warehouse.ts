// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Warehouse management module, frontend side (internal/warehouse) -
// mirrors lib/manufacturing.ts's own "types + fetch helpers, one file per
// backend package" convention (including its own apiBase()/postJSON
// helper pair, duplicated per-file rather than shared - same choice
// manufacturing.ts made over lib/inventory.ts's hand-rolled fetch calls).

export type LocationType = "aisle" | "shelf" | "bin" | "staging" | "dispatch";

export type Warehouse = {
  id: string;
  name: string;
  address?: string;
  isActive: boolean;
  createdAt: string;
};

export type Location = {
  id: string;
  warehouseId: string;
  code: string;
  name?: string;
  type: LocationType;
  parentId?: string;
  isDefault: boolean;
  isActive: boolean;
  createdAt: string;
};

export type WarehouseStockEntry = {
  locationId: string;
  locationCode: string;
  productId: string;
  productSku: string;
  quantity: string;
};

export type TransferStatus = "draft" | "in_transit" | "completed" | "cancelled";

export type InventoryTransfer = {
  id: string;
  fromLocationId: string;
  toLocationId: string;
  productId: string;
  quantity: string;
  status: TransferStatus;
  notes?: string;
  createdAt: string;
};

export type CycleCountSessionStatus = "open" | "completed";

export type CycleCountLine = {
  id: string;
  locationId: string;
  productId: string;
  systemQuantity: string;
  countedQuantity?: string;
  variance?: string;
  adjusted: boolean;
};

export type CycleCountSession = {
  id: string;
  warehouseId: string;
  status: CycleCountSessionStatus;
  createdAt: string;
  lines: CycleCountLine[];
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/warehouses`. */
export async function fetchWarehouses(token: string, firmId: string): Promise<Warehouse[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/warehouses`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Warehouse[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/warehouses/{warehouseId}`. */
export async function fetchWarehouse(token: string, firmId: string, warehouseId: string): Promise<Warehouse | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/warehouses/${encodeURIComponent(warehouseId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Warehouse;
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/warehouses/{warehouseId}/stock`. */
export async function fetchWarehouseStock(
  token: string,
  firmId: string,
  warehouseId: string,
): Promise<WarehouseStockEntry[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/warehouses/${encodeURIComponent(warehouseId)}/stock`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as WarehouseStockEntry[];
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/warehouses/{warehouseId}/locations`
 * - a flat list; the aisle -> shelf -> bin tree is built client-side from
 * each location's own `parentId`.
 */
export async function fetchWarehouseLocations(
  token: string,
  firmId: string,
  warehouseId: string,
): Promise<Location[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/warehouses/${encodeURIComponent(warehouseId)}/locations`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Location[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/inventory-transfers`. */
export async function fetchInventoryTransfers(token: string, firmId: string): Promise<InventoryTransfer[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/inventory-transfers`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as InventoryTransfer[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/inventory-transfers/{transferId}`. */
export async function fetchInventoryTransfer(
  token: string,
  firmId: string,
  transferId: string,
): Promise<InventoryTransfer | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/inventory-transfers/${encodeURIComponent(transferId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as InventoryTransfer;
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/cycle-counts` (no lines). */
export async function fetchCycleCounts(token: string, firmId: string): Promise<CycleCountSession[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/cycle-counts`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as CycleCountSession[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/cycle-counts/{sessionId}` (with lines). */
export async function fetchCycleCount(
  token: string,
  firmId: string,
  sessionId: string,
): Promise<CycleCountSession | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/cycle-counts/${encodeURIComponent(sessionId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as CycleCountSession;
  } catch {
    return null;
  }
}

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

export type CreateWarehouseInput = {
  name: string;
  address?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/warehouses` (owner-only). */
export async function createWarehouse(
  token: string,
  firmId: string,
  input: CreateWarehouseInput,
): Promise<ApiResult<"warehouse", Warehouse>> {
  return postJSON(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/warehouses`, "POST", token, "warehouse", input);
}

export type UpdateWarehouseInput = Partial<{
  name: string;
  address: string;
  isActive: boolean;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/warehouses/{warehouseId}` (owner-only). */
export async function updateWarehouse(
  token: string,
  firmId: string,
  warehouseId: string,
  patch: UpdateWarehouseInput,
): Promise<ApiResult<"warehouse", Warehouse>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/warehouses/${encodeURIComponent(warehouseId)}`,
    "PATCH",
    token,
    "warehouse",
    patch,
  );
}

export type CreateLocationInput = {
  code: string;
  name?: string;
  type?: LocationType;
  parentId?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/warehouses/{warehouseId}/locations` (owner-only). */
export async function createLocation(
  token: string,
  firmId: string,
  warehouseId: string,
  input: CreateLocationInput,
): Promise<ApiResult<"location", Location>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/warehouses/${encodeURIComponent(warehouseId)}/locations`,
    "POST",
    token,
    "location",
    input,
  );
}

export type UpdateLocationInput = Partial<{
  name: string;
  type: LocationType;
  isActive: boolean;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/locations/{locationId}` (owner-only). */
export async function updateLocation(
  token: string,
  firmId: string,
  locationId: string,
  patch: UpdateLocationInput,
): Promise<ApiResult<"location", Location>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/locations/${encodeURIComponent(locationId)}`,
    "PATCH",
    token,
    "location",
    patch,
  );
}

export type CreateInventoryTransferInput = {
  fromLocationId: string;
  toLocationId: string;
  productId: string;
  quantity: string;
  notes?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/inventory-transfers` (owner-only). Starts in status `draft`. */
export async function createInventoryTransfer(
  token: string,
  firmId: string,
  input: CreateInventoryTransferInput,
): Promise<ApiResult<"transfer", InventoryTransfer>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/inventory-transfers`,
    "POST",
    token,
    "transfer",
    input,
  );
}

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/inventory-transfers/{transferId}/complete`
 * (owner-only, no body). Atomically deducts from `fromLocationId` and adds
 * to `toLocationId` - can fail with a plain-text 400 body (e.g.
 * "insufficient stock: ...") if the source location doesn't have enough.
 */
export async function completeInventoryTransfer(
  token: string,
  firmId: string,
  transferId: string,
): Promise<ApiResult<"transfer", InventoryTransfer>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/inventory-transfers/${encodeURIComponent(transferId)}/complete`,
    "POST",
    token,
    "transfer",
  );
}

/** Calls the Go backend's `POST /api/firms/{firmId}/inventory-transfers/{transferId}/cancel` (owner-only, no body). */
export async function cancelInventoryTransfer(
  token: string,
  firmId: string,
  transferId: string,
): Promise<ApiResult<"transfer", InventoryTransfer>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/inventory-transfers/${encodeURIComponent(transferId)}/cancel`,
    "POST",
    token,
    "transfer",
  );
}

export type CreateCycleCountInput = {
  warehouseId: string;
  lines: { locationId: string; productId: string }[];
};

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/cycle-counts`
 * (owner-only). Snapshots `systemQuantity` for each line from current
 * stock server-side - the session starts in status `open`.
 */
export async function createCycleCount(
  token: string,
  firmId: string,
  input: CreateCycleCountInput,
): Promise<ApiResult<"session", CycleCountSession>> {
  return postJSON(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/cycle-counts`, "POST", token, "session", input);
}

/**
 * Calls the Go backend's
 * `POST /api/firms/{firmId}/cycle-counts/{sessionId}/lines/{lineId}/count`
 * (owner-only). `countedQuantity` is a decimal string (e.g. "7" or "7.5").
 */
export async function recordCycleCountLine(
  token: string,
  firmId: string,
  sessionId: string,
  lineId: string,
  countedQuantity: string,
): Promise<ApiResult<"line", CycleCountLine>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/cycle-counts/${encodeURIComponent(sessionId)}/lines/${encodeURIComponent(lineId)}/count`,
    "POST",
    token,
    "line",
    { countedQuantity },
  );
}

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/cycle-counts/{sessionId}/complete`
 * (owner-only). When `adjust` is true, every line with a recorded count
 * and non-zero variance gets a real stock adjustment plus a balanced
 * journal entry, server-side.
 */
export async function completeCycleCount(
  token: string,
  firmId: string,
  sessionId: string,
  adjust: boolean,
): Promise<ApiResult<"session", CycleCountSession>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/cycle-counts/${encodeURIComponent(sessionId)}/complete`,
    "POST",
    token,
    "session",
    { adjust },
  );
}
