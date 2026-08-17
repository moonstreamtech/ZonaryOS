// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Cost centers module (internal/costcenter), frontend side - mirrors
// lib/contract.ts's own "types + fetch helpers, one file per backend
// package, postJSON generic helper" convention.

export type CostCenter = {
  id: string;
  code: string;
  name: string;
  parentId?: string;
  isActive: boolean;
  createdAt: string;
};

// CostCenterPnLLine is one row of `GET .../reports/cost-center-pnl` -
// costCenterId is absent/null for the "Unassigned" bucket (journal
// entries with no cost center on them), matching
// internal/costcenter.CostCenterPnLLine's own doc comment. revenue/
// expenses/net are decimal strings (e.g. "1000.0000"), same convention
// as every other money field in this codebase.
export type CostCenterPnLLine = {
  costCenterId?: string;
  name: string;
  revenue: string;
  expenses: string;
  net: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

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
    const value = res.status === 204 ? (undefined as V) : ((await res.json()) as V);
    return { ok: true, [key]: value } as ApiResult<T, V>;
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/cost-centers`. */
export async function fetchCostCenters(token: string, firmId: string): Promise<CostCenter[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/cost-centers`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as CostCenter[];
  } catch {
    return null;
  }
}

export type CreateCostCenterInput = {
  code: string;
  name: string;
  parentId?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/cost-centers` (owner-only). */
export async function createCostCenter(
  token: string,
  firmId: string,
  input: CreateCostCenterInput,
): Promise<ApiResult<"costCenter", CostCenter>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/cost-centers`,
    "POST",
    token,
    "costCenter",
    input,
  );
}

export type UpdateCostCenterInput = Partial<{
  name: string;
  isActive: boolean;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/cost-centers/{costCenterId}` (owner-only). */
export async function updateCostCenter(
  token: string,
  firmId: string,
  costCenterId: string,
  patch: UpdateCostCenterInput,
): Promise<ApiResult<"costCenter", CostCenter>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/cost-centers/${encodeURIComponent(costCenterId)}`,
    "PATCH",
    token,
    "costCenter",
    patch,
  );
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/reports/cost-center-pnl?from=&to=`
 * (both optional, RFC3339 timestamps) - the /financials page's "Cost
 * Center P&L" tab data source. Returns null on failure, same convention
 * as lib/accounting.ts's fetchPnLReport.
 */
export async function fetchCostCenterPnL(
  token: string,
  firmId: string,
  opts: { from?: string; to?: string } = {},
): Promise<CostCenterPnLLine[] | null> {
  try {
    const params = new URLSearchParams();
    if (opts.from) params.set("from", opts.from);
    if (opts.to) params.set("to", opts.to);
    const qs = params.toString();
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/reports/cost-center-pnl${qs ? `?${qs}` : ""}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as CostCenterPnLLine[];
  } catch {
    return null;
  }
}
