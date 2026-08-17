// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Multi-company consolidation module (internal/consolidation), frontend
// side - mirrors lib/budget.ts's own "types + fetch helpers, one file per
// backend package, postJSON generic helper" convention. Every
// /firm-groups/... endpoint is internal/platformadmin.Allowlist-gated,
// same posture as lib/platformAdmin.ts's own fetchPlatformAdminFirms and
// lib/exchangeRates.ts's own createExchangeRate: a non-2xx response
// (including the 404 a non-allowlisted caller gets) is not distinguished
// from any other failure for the read helpers below, and surfaced as a
// generic { ok: false } for the write helpers, same as postJSON already
// does elsewhere. fetchFirmGroupForFirm is the one exception - it calls
// the firm-member-gated GET /api/firms/{firmId}/group, not a
// platform-admin route, and a `null` JSON body (firm not in any group) is
// a valid, distinct outcome from "failed to load" (see
// internal/consolidation/handlers.go's handleGetFirmGroupForFirm doc
// comment) - modeled here as a `{ ok: true, group: null }` vs.
// `{ ok: false }` result rather than collapsing both to `null`.

export type FirmGroupRole = "parent" | "subsidiary" | "affiliate";

export type FirmGroupMember = {
  firmId: string;
  firmName: string;
  role: FirmGroupRole;
  ownershipPct?: string;
};

export type FirmGroup = {
  id: string;
  name: string;
  createdAt: string;
  members: FirmGroupMember[];
};

export type FirmGroupMemberWrite = {
  id: string;
  groupId: string;
  firmId: string;
  role: FirmGroupRole;
  ownershipPct?: string;
  joinedAt: string;
};

export type IntercompanyTransaction = {
  id: string;
  groupId: string;
  fromFirmId: string;
  toFirmId: string;
  amount: string;
  currency: string;
  description: string;
  fromJournalEntryId?: string;
  toJournalEntryId?: string;
  createdAt: string;
};

export type ConsolidatedPnLFirmLine = {
  firmId: string;
  firmName: string;
  revenue: string;
  expenses: string;
  netIncome: string;
};

export type ConsolidatedPnL = {
  groupId: string;
  from?: string;
  to?: string;
  firms: ConsolidatedPnLFirmLine[];
  totalRevenue: string;
  totalExpenses: string;
  netIncome: string;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

type ApiResult<T extends string, V> = ({ ok: true } & Record<T, V>) | { ok: false; error: string; status: number };

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

/** Calls the Go backend's `GET /api/platform-admin/firm-groups` (platform-admin only). */
export async function fetchFirmGroups(token: string): Promise<FirmGroup[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/platform-admin/firm-groups`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as FirmGroup[];
  } catch {
    return null;
  }
}

export type CreateFirmGroupInput = {
  name: string;
};

/** Calls the Go backend's `POST /api/platform-admin/firm-groups` (platform-admin only). */
export async function createFirmGroup(
  token: string,
  input: CreateFirmGroupInput,
): Promise<ApiResult<"group", FirmGroup>> {
  return postJSON(`${apiBase()}/api/platform-admin/firm-groups`, "POST", token, "group", input);
}

export type AddGroupMemberInput = {
  firmId: string;
  role: FirmGroupRole;
  ownershipPct?: string;
};

/**
 * Calls the Go backend's
 * `POST /api/platform-admin/firm-groups/{groupId}/members` (platform-admin
 * only).
 */
export async function addGroupMember(
  token: string,
  groupId: string,
  input: AddGroupMemberInput,
): Promise<ApiResult<"member", FirmGroupMemberWrite>> {
  return postJSON(
    `${apiBase()}/api/platform-admin/firm-groups/${encodeURIComponent(groupId)}/members`,
    "POST",
    token,
    "member",
    input,
  );
}

export type IntercompanyTransferInput = {
  fromFirmId: string;
  toFirmId: string;
  amount: string;
  currency: string;
  description: string;
};

/**
 * Calls the Go backend's
 * `POST /api/platform-admin/firm-groups/{groupId}/intercompany-transfer`
 * (platform-admin only) - posts a real journal entry into both member
 * firms atomically.
 */
export async function postIntercompanyTransfer(
  token: string,
  groupId: string,
  input: IntercompanyTransferInput,
): Promise<ApiResult<"transaction", IntercompanyTransaction>> {
  return postJSON(
    `${apiBase()}/api/platform-admin/firm-groups/${encodeURIComponent(groupId)}/intercompany-transfer`,
    "POST",
    token,
    "transaction",
    input,
  );
}

export type ConsolidatedPnLFilter = {
  from?: string;
  to?: string;
};

/**
 * Calls the Go backend's
 * `GET /api/platform-admin/firm-groups/{groupId}/consolidated-pnl`
 * (platform-admin only). `from`/`to` must be RFC3339 timestamps, same
 * convention as lib/accounting.ts's own fetchPnLReport.
 */
export async function fetchConsolidatedPnL(
  token: string,
  groupId: string,
  filter?: ConsolidatedPnLFilter,
): Promise<ConsolidatedPnL | null> {
  try {
    const params = new URLSearchParams();
    if (filter?.from) params.set("from", filter.from);
    if (filter?.to) params.set("to", filter.to);
    const qs = params.toString();
    const res = await fetch(
      `${apiBase()}/api/platform-admin/firm-groups/${encodeURIComponent(groupId)}/consolidated-pnl${qs ? `?${qs}` : ""}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as ConsolidatedPnL;
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/group` - firm-member
 * gated (NOT platform-admin gated), unlike every other helper in this
 * file. Returns `{ ok: true, group: null }` when the response body is a
 * JSON `null` (the firm isn't in any group - not an error, see
 * internal/consolidation/handlers.go's handleGetFirmGroupForFirm doc
 * comment), and `{ ok: false }` for a non-2xx response or network
 * failure.
 */
export async function fetchFirmGroupForFirm(
  token: string,
  firmId: string,
): Promise<{ ok: true; group: FirmGroup | null } | { ok: false }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/group`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return { ok: false };
    const body = (await res.json()) as FirmGroup | null;
    return { ok: true, group: body };
  } catch {
    return { ok: false };
  }
}
