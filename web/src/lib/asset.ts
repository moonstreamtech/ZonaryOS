// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Asset management, maintenance, and facility operations module, frontend
// side (internal/asset) - mirrors lib/warehouse.ts's own "types + fetch
// helpers, one file per backend package, postJSON generic helper"
// convention. Date fields (purchaseDate, warrantyExpires, lastDoneAt,
// performedAt, nextServiceDate) are plain "YYYY-MM-DD" strings, matching
// internal/asset/handlers.go's own formatDate helper - never RFC3339.

export type AssetType = "equipment" | "vehicle" | "property" | "tool" | "other";
export type AssetStatus = "active" | "maintenance" | "retired" | "disposed";

export type Asset = {
  id: string;
  name: string;
  assetNumber: string;
  type: AssetType;
  status: AssetStatus;
  locationId?: string;
  assignedToPersonId?: string;
  purchaseDate?: string;
  purchasePrice?: string;
  currentValue?: string;
  depreciationRate?: string;
  serialNumber?: string;
  warrantyExpires?: string;
  notes?: string;
  customFields: Record<string, unknown>;
  createdAt: string;
};

export type MaintenanceSchedule = {
  id: string;
  assetId: string;
  name: string;
  intervalDays: number;
  lastDoneAt?: string;
  nextDueAt?: string;
  assignedToPersonId?: string;
  isActive: boolean;
  createdAt: string;
};

export type MaintenanceRecord = {
  id: string;
  assetId: string;
  scheduleId?: string;
  performedAt: string;
  performedByPersonId?: string;
  description: string;
  cost?: string;
  nextServiceDate?: string;
  createdAt: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/** Calls the Go backend's `GET /api/firms/{firmId}/assets`. */
export async function fetchAssets(token: string, firmId: string): Promise<Asset[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/assets`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Asset[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/assets/{assetId}`. */
export async function fetchAsset(token: string, firmId: string, assetId: string): Promise<Asset | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/assets/${encodeURIComponent(assetId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Asset;
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/assets/{assetId}/maintenance-schedules`. */
export async function fetchMaintenanceSchedules(
  token: string,
  firmId: string,
  assetId: string,
): Promise<MaintenanceSchedule[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/assets/${encodeURIComponent(assetId)}/maintenance-schedules`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as MaintenanceSchedule[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/assets/{assetId}/maintenance-records`. */
export async function fetchMaintenanceRecords(
  token: string,
  firmId: string,
  assetId: string,
): Promise<MaintenanceRecord[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/assets/${encodeURIComponent(assetId)}/maintenance-records`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as MaintenanceRecord[];
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

export type CreateAssetInput = {
  name: string;
  assetNumber: string;
  type: AssetType;
  status?: AssetStatus;
  locationId?: string;
  assignedToPersonId?: string;
  purchaseDate?: string;
  purchasePrice?: string;
  currentValue?: string;
  depreciationRate?: string;
  serialNumber?: string;
  warrantyExpires?: string;
  notes?: string;
  customFields?: Record<string, unknown>;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/assets` (owner-only). */
export async function createAsset(
  token: string,
  firmId: string,
  input: CreateAssetInput,
): Promise<ApiResult<"asset", Asset>> {
  return postJSON(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/assets`, "POST", token, "asset", input);
}

export type UpdateAssetInput = Partial<{
  name: string;
  status: AssetStatus;
  locationId: string;
  hasLocationId: boolean;
  assignedToPersonId: string;
  hasAssignedToPersonId: boolean;
  purchaseDate: string;
  purchasePrice: string;
  currentValue: string;
  depreciationRate: string;
  serialNumber: string;
  warrantyExpires: string;
  notes: string;
  customFields: Record<string, unknown>;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/assets/{assetId}` (owner-only). */
export async function updateAsset(
  token: string,
  firmId: string,
  assetId: string,
  patch: UpdateAssetInput,
): Promise<ApiResult<"asset", Asset>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/assets/${encodeURIComponent(assetId)}`,
    "PATCH",
    token,
    "asset",
    patch,
  );
}

export type CreateMaintenanceScheduleInput = {
  name: string;
  intervalDays: number;
  lastDoneAt?: string;
  assignedToPersonId?: string;
};

/**
 * Calls the Go backend's
 * `POST /api/firms/{firmId}/assets/{assetId}/maintenance-schedules`
 * (owner-only).
 */
export async function createMaintenanceSchedule(
  token: string,
  firmId: string,
  assetId: string,
  input: CreateMaintenanceScheduleInput,
): Promise<ApiResult<"schedule", MaintenanceSchedule>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/assets/${encodeURIComponent(assetId)}/maintenance-schedules`,
    "POST",
    token,
    "schedule",
    input,
  );
}

export type UpdateMaintenanceScheduleInput = Partial<{
  name: string;
  intervalDays: number;
  assignedToPersonId: string;
  hasAssignedToPersonId: boolean;
  isActive: boolean;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/maintenance-schedules/{scheduleId}` (owner-only). */
export async function updateMaintenanceSchedule(
  token: string,
  firmId: string,
  scheduleId: string,
  patch: UpdateMaintenanceScheduleInput,
): Promise<ApiResult<"schedule", MaintenanceSchedule>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/maintenance-schedules/${encodeURIComponent(scheduleId)}`,
    "PATCH",
    token,
    "schedule",
    patch,
  );
}

export type CreateMaintenanceRecordInput = {
  scheduleId?: string;
  performedAt: string;
  performedByPersonId?: string;
  description: string;
  cost?: string;
  nextServiceDate?: string;
};

/**
 * Calls the Go backend's
 * `POST /api/firms/{firmId}/assets/{assetId}/maintenance-records`
 * (owner-only). If `scheduleId` is set, the schedule's `lastDoneAt`
 * advances to `performedAt` server-side (and `nextDueAt` recomputes
 * automatically). If `cost` is set (non-empty, non-zero), a real journal
 * entry posts server-side too.
 */
export async function createMaintenanceRecord(
  token: string,
  firmId: string,
  assetId: string,
  input: CreateMaintenanceRecordInput,
): Promise<ApiResult<"record", MaintenanceRecord>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/assets/${encodeURIComponent(assetId)}/maintenance-records`,
    "POST",
    token,
    "record",
    input,
  );
}
