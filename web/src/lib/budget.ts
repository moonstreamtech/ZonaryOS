// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Budget management module (internal/budget), frontend side - mirrors
// lib/contract.ts's own "types + fetch helpers, one file per backend
// package, postJSON generic helper" convention. periodStart/periodEnd
// are plain "YYYY-MM-DD" strings, matching internal/budget/handlers.go's
// own toBudgetResponse (b.PeriodStart.Format("2006-01-02")) - never
// RFC3339. approvedAt IS a genuine RFC3339 timestamp (server-set only,
// via the approve endpoint - no UI here sets it directly).

export type BudgetPeriodType = "monthly" | "quarterly" | "annual";
export type BudgetStatus = "draft" | "approved" | "active" | "closed";

export type Budget = {
  id: string;
  name: string;
  periodType: BudgetPeriodType;
  periodStart: string;
  periodEnd: string;
  status: BudgetStatus;
  currency: string;
  notes?: string;
  approvedBy?: string;
  approvedAt?: string;
  createdAt: string;
};

export type BudgetLine = {
  id: string;
  budgetId: string;
  accountId: string;
  plannedAmount: string;
  notes?: string;
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

/** Calls the Go backend's `GET /api/firms/{firmId}/budgets`. */
export async function fetchBudgets(token: string, firmId: string): Promise<Budget[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/budgets`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Budget[];
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/budgets/{budgetId}`. */
export async function fetchBudget(token: string, firmId: string, budgetId: string): Promise<Budget | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/budgets/${encodeURIComponent(budgetId)}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as Budget;
  } catch {
    return null;
  }
}

/** Calls the Go backend's `GET /api/firms/{firmId}/budgets/{budgetId}/lines`. */
export async function fetchBudgetLines(
  token: string,
  firmId: string,
  budgetId: string,
): Promise<BudgetLine[] | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/budgets/${encodeURIComponent(budgetId)}/lines`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as BudgetLine[];
  } catch {
    return null;
  }
}

export type CreateBudgetInput = {
  name: string;
  periodType: BudgetPeriodType;
  periodStart: string;
  periodEnd: string;
  status?: BudgetStatus;
  currency?: string;
  notes?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/budgets` (owner-only). */
export async function createBudget(
  token: string,
  firmId: string,
  input: CreateBudgetInput,
): Promise<ApiResult<"budget", Budget>> {
  return postJSON(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/budgets`, "POST", token, "budget", input);
}

export type UpdateBudgetInput = Partial<{
  name: string;
  status: BudgetStatus;
  notes: string;
  currency: string;
}>;

/** Calls the Go backend's `PATCH /api/firms/{firmId}/budgets/{budgetId}` (owner-only). */
export async function updateBudget(
  token: string,
  firmId: string,
  budgetId: string,
  patch: UpdateBudgetInput,
): Promise<ApiResult<"budget", Budget>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/budgets/${encodeURIComponent(budgetId)}`,
    "PATCH",
    token,
    "budget",
    patch,
  );
}

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/budgets/{budgetId}/approve`
 * (owner-only, drafts -> approved). If activating this budget's period
 * overlaps another already-'active' budget, the backend flips that other
 * budget back to 'approved' automatically in the same transaction - this
 * call only reports success/failure for THIS budget, it doesn't surface
 * which other budgets (if any) got flipped.
 */
export async function approveBudget(
  token: string,
  firmId: string,
  budgetId: string,
): Promise<ApiResult<"budget", Budget>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/budgets/${encodeURIComponent(budgetId)}/approve`,
    "POST",
    token,
    "budget",
  );
}

export type CreateBudgetLineInput = {
  accountId: string;
  plannedAmount: string;
  notes?: string;
};

/** Calls the Go backend's `POST /api/firms/{firmId}/budgets/{budgetId}/lines` (owner-only). */
export async function createBudgetLine(
  token: string,
  firmId: string,
  budgetId: string,
  input: CreateBudgetLineInput,
): Promise<ApiResult<"line", BudgetLine>> {
  return postJSON(
    `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/budgets/${encodeURIComponent(budgetId)}/lines`,
    "POST",
    token,
    "line",
    input,
  );
}
