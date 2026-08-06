// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

// Vision §3's financial management core, frontend side: the chart of
// accounts (internal/accounting.Account) and the journal
// (internal/accounting.JournalEntry) - mirrors lib/workflow.ts/
// lib/auditlog.ts's own "types + fetch helpers, one file per backend
// package" convention.

export type AccountType = "asset" | "liability" | "equity" | "revenue" | "expense";

export type Account = {
  id: string;
  code: string;
  name: string;
  type: AccountType;
  parentId?: string;
  currency: string;
  isActive: boolean;
  createdAt: string;
};

export type JournalLine = {
  accountId: string;
  accountCode: string;
  accountName: string;
  side: "debit" | "credit";
  amount: string;
  currency: string;
};

export type JournalEntry = {
  id: string;
  postedAt: string;
  description: string;
  sourceType?: string;
  sourceId?: string;
  createdBy: string;
  lines: JournalLine[];
};

export type ListJournalEntriesPage = {
  entries: JournalEntry[];
  total: number;
};

function apiBase(): string {
  return process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/accounts` - the chart of
 * accounts management page's data source. Member-gated on the backend
 * (see internal/accounting.ListAccounts); returns null on any failure,
 * same swallow-to-null convention as lib/workflow.ts's own read helpers.
 */
export async function fetchAccounts(token: string, firmId: string): Promise<Account[] | null> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/accounts`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    if (!res.ok) return null;
    return (await res.json()) as Account[];
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/accounts/{accountId}/balance`.
 * Returns null on failure, same convention as fetchAccounts.
 */
export async function fetchAccountBalance(
  token: string,
  firmId: string,
  accountId: string,
): Promise<string | null> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/accounts/${encodeURIComponent(accountId)}/balance`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    const body = (await res.json()) as { balance: string };
    return body.balance;
  } catch {
    return null;
  }
}

export type CreateAccountInput = {
  code: string;
  name: string;
  type: AccountType;
  parentId?: string;
  currency?: string;
};

/**
 * Calls the Go backend's `POST /api/firms/{firmId}/accounts` (owner-only).
 * Failures are surfaced to the caller (not swallowed to null) - the
 * accounts management form needs to show why creation didn't go through
 * (e.g. 409 duplicate code, 403 not-owner), same convention as
 * lib/workflow.ts's defineWorkflow/createInstance.
 */
export async function createAccount(
  token: string,
  firmId: string,
  input: CreateAccountInput,
): Promise<{ ok: true; account: Account } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(`${apiBase()}/api/firms/${encodeURIComponent(firmId)}/accounts`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(input),
      cache: "no-store",
    });
    if (!res.ok) {
      const text = await res.text();
      return { ok: false, error: text || `Request failed (${res.status})`, status: res.status };
    }
    return { ok: true, account: (await res.json()) as Account };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

export type UpdateAccountInput = {
  name?: string;
  isActive?: boolean;
};

/**
 * Calls the Go backend's `PATCH /api/firms/{firmId}/accounts/{accountId}`
 * (owner-only) - a partial update, same "omitted field leaves that column
 * unchanged" contract internal/accounting.AccountUpdate documents.
 */
export async function updateAccount(
  token: string,
  firmId: string,
  accountId: string,
  patch: UpdateAccountInput,
): Promise<{ ok: true; account: Account } | { ok: false; error: string; status: number }> {
  try {
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/accounts/${encodeURIComponent(accountId)}`,
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
    return { ok: true, account: (await res.json()) as Account };
  } catch {
    return { ok: false, error: "network error", status: 0 };
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/journal-entries` - the
 * /financials page's recent-entries data source. Member-gated on the
 * backend. Returns null on failure, same convention as fetchAccounts.
 */
export async function fetchJournalEntries(
  token: string,
  firmId: string,
  opts: { limit?: number; offset?: number } = {},
): Promise<ListJournalEntriesPage | null> {
  try {
    const params = new URLSearchParams();
    if (opts.limit) params.set("limit", String(opts.limit));
    if (opts.offset) params.set("offset", String(opts.offset));
    const qs = params.toString();
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/journal-entries${qs ? `?${qs}` : ""}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    const entries = (await res.json()) as JournalEntry[];
    const total = Number(res.headers.get("X-Total-Count") ?? entries.length);
    return { entries, total: Number.isFinite(total) ? total : entries.length };
  } catch {
    return null;
  }
}
