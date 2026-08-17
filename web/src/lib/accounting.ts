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

// ReportLine is internal/accounting.ReportLine's wire shape - one
// account's own contribution to a P&L or Balance Sheet report, shared by
// both report types below (PnLReport.revenue/expenses,
// BalanceSheetReport.assets/liabilities/equity). budgetedAmount/
// variance/variancePercent (budget management batch) are only ever
// present on PnLReport.revenue/expenses lines, and only when an
// 'active' budget covers the requested period - see
// internal/accounting/reports.go's attachBudgetVarianceTx. variance =
// amount - budgetedAmount (positive means over budget for an expense
// line, under budget for a revenue line - the account type tells you
// which).
export type ReportLine = {
  accountId: string;
  code: string;
  name: string;
  amount: string;
  budgetedAmount?: string;
  variance?: string;
  variancePercent?: string;
};

export type PnLReport = {
  from?: string;
  to?: string;
  revenue: ReportLine[];
  expenses: ReportLine[];
  totalRevenue: string;
  totalExpenses: string;
  netIncome: string;
};

export type BalanceSheetReport = {
  asOf: string;
  assets: ReportLine[];
  liabilities: ReportLine[];
  equity: ReportLine[];
  // currentEarnings is net income accrued since the last closing entry
  // (there is no period-closing mechanism yet), folded into totalEquity -
  // see internal/accounting.GetBalanceSheet's own doc comment for why
  // this is the correct interim accounting treatment, not a shortcut.
  currentEarnings: string;
  totalAssets: string;
  totalLiabilities: string;
  totalEquity: string;
  // balanced/difference is the literal Assets = Liabilities + Equity
  // check, computed server-side - a genuine data-integrity signal (see
  // GetBalanceSheet's doc comment), not merely cosmetic.
  balanced: boolean;
  difference: string;
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
 * Calls the Go backend's `GET /api/firms/{firmId}/reports/pnl?from=&to=`
 * (both optional, RFC3339 timestamps) - the /financials page's P&L tab
 * data source. Returns null on failure, same convention as fetchAccounts.
 */
export async function fetchPnLReport(
  token: string,
  firmId: string,
  opts: { from?: string; to?: string } = {},
): Promise<PnLReport | null> {
  try {
    const params = new URLSearchParams();
    if (opts.from) params.set("from", opts.from);
    if (opts.to) params.set("to", opts.to);
    const qs = params.toString();
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/reports/pnl${qs ? `?${qs}` : ""}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as PnLReport;
  } catch {
    return null;
  }
}

/**
 * Calls the Go backend's `GET /api/firms/{firmId}/reports/balance-sheet?as_of=`
 * (optional RFC3339 timestamp; the backend defaults to now when omitted) -
 * the /financials page's Balance Sheet tab data source. Returns null on
 * failure, same convention as fetchAccounts.
 */
export async function fetchBalanceSheetReport(
  token: string,
  firmId: string,
  opts: { asOf?: string } = {},
): Promise<BalanceSheetReport | null> {
  try {
    const params = new URLSearchParams();
    if (opts.asOf) params.set("as_of", opts.asOf);
    const qs = params.toString();
    const res = await fetch(
      `${apiBase()}/api/firms/${encodeURIComponent(firmId)}/reports/balance-sheet${qs ? `?${qs}` : ""}`,
      { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" },
    );
    if (!res.ok) return null;
    return (await res.json()) as BalanceSheetReport;
  } catch {
    return null;
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
