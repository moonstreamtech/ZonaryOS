// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import {
  fetchAccounts,
  fetchAccountBalance,
  fetchJournalEntries,
  fetchPnLReport,
  fetchBalanceSheetReport,
  type ReportLine,
} from "@/lib/accounting";
import { fetchReceivablesAging } from "@/lib/invoicing";
import { fetchCostCenterPnL } from "@/lib/costcenter";

type PageProps = {
  params: Promise<{ locale: string }>;
  searchParams: Promise<{ tab?: string; from?: string; to?: string; asOf?: string }>;
};

type Tab = "balances" | "pnl" | "balance-sheet" | "aging" | "cost-center-pnl";

// The date `<input type="date">` fields below submit a plain "YYYY-MM-DD"
// via a real GET navigation (see this file's own doc comment on why),
// but the backend's ?from=/?to=/?as_of= all parse RFC3339 timestamps
// (see internal/accounting/handlers.go's parseReportDateRange) - these
// two helpers bridge that gap right before the fetch call, converting a
// picked calendar day into its start-of-day/end-of-day UTC instant so a
// "from"/"as of" day is inclusive of the whole day, not just its first
// instant. The raw "YYYY-MM-DD" itself is what stays in the URL (and
// what the input's defaultValue reads back), never the expanded
// timestamp - only the fetch call sees the RFC3339 form.
function startOfDayUTC(dateOnly?: string): string | undefined {
  if (!dateOnly) return undefined;
  return `${dateOnly}T00:00:00Z`;
}
function endOfDayUTC(dateOnly?: string): string | undefined {
  if (!dateOnly) return undefined;
  return `${dateOnly}T23:59:59Z`;
}

// Vision §3's financial management core: a functional (not polished)
// financials page with three tabs - Account Balances (the original,
// unchanged data source), Profit & Loss, and Balance Sheet (both new,
// computed views over internal/accounting.GetProfitAndLoss/GetBalanceSheet).
// The active tab and its own date filters live entirely in the URL's
// search params (?tab=/?from=/?to=/?asOf=), the same "server-rendered,
// no client state" convention WorkflowDefinitionView's own ?page=/?q=
// pagination already uses - a plain HTML `<form method="get">` submits a
// date range/as-of date as a real navigation, not a client-side fetch.
// Member-gated throughout (not owner-gated): reading any of these reports
// is ordinary firm data visibility, the same tier as ListJournalEntries -
// only chart-of-accounts management (/settings/accounts) and posting a
// manual entry are owner-only.
export default async function FinancialsPage({ params, searchParams }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Financials");
  const sp = await searchParams;
  const tab: Tab =
    sp.tab === "pnl"
      ? "pnl"
      : sp.tab === "balance-sheet"
        ? "balance-sheet"
        : sp.tab === "aging"
          ? "aging"
          : sp.tab === "cost-center-pnl"
            ? "cost-center-pnl"
            : "balances";

  const { sessionToken, firm } = await requireFirmContext(locale);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-4">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("title")}
        </h1>
        <nav className="flex gap-2 text-sm">
          <TabLink locale={locale} tab="balances" active={tab === "balances"} label={t("tabBalances")} />
          <TabLink locale={locale} tab="pnl" active={tab === "pnl"} label={t("tabPnL")} />
          <TabLink
            locale={locale}
            tab="balance-sheet"
            active={tab === "balance-sheet"}
            label={t("tabBalanceSheet")}
          />
          <TabLink locale={locale} tab="aging" active={tab === "aging"} label={t("tabAging")} />
          <TabLink
            locale={locale}
            tab="cost-center-pnl"
            active={tab === "cost-center-pnl"}
            label={t("tabCostCenterPnL")}
          />
        </nav>
      </div>

      {tab === "balances" && (
        <BalancesTab sessionToken={sessionToken} firmId={firm.firmId} />
      )}
      {tab === "pnl" && (
        <PnLTab sessionToken={sessionToken} firmId={firm.firmId} from={sp.from} to={sp.to} />
      )}
      {tab === "balance-sheet" && (
        <BalanceSheetTab sessionToken={sessionToken} firmId={firm.firmId} asOf={sp.asOf} />
      )}
      {tab === "aging" && <AgingTab sessionToken={sessionToken} firmId={firm.firmId} />}
      {tab === "cost-center-pnl" && (
        <CostCenterPnLTab sessionToken={sessionToken} firmId={firm.firmId} from={sp.from} to={sp.to} />
      )}
    </main>
  );
}

async function TabLink({
  locale,
  tab,
  active,
  label,
}: {
  locale: string;
  tab: Tab;
  active: boolean;
  label: string;
}) {
  return (
    <a
      href={`/${locale}/financials?tab=${tab}`}
      data-permission-public="true"
      className={
        active
          ? "rounded-full bg-black px-3 py-1.5 font-medium text-white dark:bg-zinc-50 dark:text-black"
          : "rounded-full border border-zinc-300 px-3 py-1.5 text-zinc-700 hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-300 dark:hover:bg-zinc-900"
      }
    >
      {label}
    </a>
  );
}

async function BalancesTab({ sessionToken, firmId }: { sessionToken: string; firmId: string }) {
  const t = await getTranslations("Financials");

  const [accounts, entriesPage] = await Promise.all([
    fetchAccounts(sessionToken, firmId),
    fetchJournalEntries(sessionToken, firmId, { limit: 25 }),
  ]);

  const balances =
    accounts !== null
      ? await Promise.all(
          accounts.map(async (a) => ({
            account: a,
            balance: await fetchAccountBalance(sessionToken, firmId, a.id),
          })),
        )
      : null;

  return (
    <>
      <div className="flex w-full max-w-4xl flex-col gap-4">
        <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("balancesTitle")}
        </h2>
        {balances === null ? (
          <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
        ) : balances.length === 0 ? (
          <p className="text-zinc-600 dark:text-zinc-400">{t("noAccounts")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 font-medium">{t("columnCode")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnAccount")}</th>
                  <th className="py-2 font-medium">{t("columnBalance")}</th>
                </tr>
              </thead>
              <tbody>
                {balances.map(({ account, balance }) => (
                  <tr
                    key={account.id}
                    className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50"
                  >
                    <td className="py-2 pr-4 font-mono text-xs whitespace-nowrap">
                      {account.code}
                    </td>
                    <td className="py-2 pr-4">{account.name}</td>
                    <td className="py-2 font-mono">
                      {balance !== null ? `${balance} ${account.currency}` : t("loadError")}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="flex w-full max-w-4xl flex-col gap-4">
        <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("recentEntriesTitle")}
        </h2>
        {entriesPage === null ? (
          <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
        ) : entriesPage.entries.length === 0 ? (
          <p className="text-zinc-600 dark:text-zinc-400">{t("noEntries")}</p>
        ) : (
          <ul className="flex flex-col gap-3">
            {entriesPage.entries.map((entry) => (
              <li
                key={entry.id}
                className="rounded-md border border-zinc-300 p-3 text-sm dark:border-zinc-700"
              >
                <div className="flex flex-wrap items-baseline justify-between gap-2">
                  <span className="font-medium text-black dark:text-zinc-50">
                    {entry.description}
                  </span>
                  <span className="text-xs text-zinc-500 dark:text-zinc-400">
                    {new Date(entry.postedAt).toLocaleString()}
                  </span>
                </div>
                <ul className="mt-2 flex flex-col gap-1">
                  {entry.lines.map((line, idx) => (
                    <li
                      key={idx}
                      className="flex items-center justify-between font-mono text-xs text-zinc-700 dark:text-zinc-300"
                    >
                      <span>
                        {line.side === "debit" ? t("debitLabel") : t("creditLabel")}{" "}
                        {line.accountCode} — {line.accountName}
                      </span>
                      <span>
                        {line.amount} {line.currency}
                      </span>
                    </li>
                  ))}
                </ul>
              </li>
            ))}
          </ul>
        )}
      </div>
    </>
  );
}

function reportLineRows(lines: ReportLine[]) {
  return lines.map((l) => (
    <tr key={l.accountId} className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50">
      <td className="py-2 pr-4 font-mono text-xs whitespace-nowrap">{l.code}</td>
      <td className="py-2 pr-4">{l.name}</td>
      <td className="py-2 font-mono">{l.amount}</td>
    </tr>
  ));
}

async function PnLTab({
  sessionToken,
  firmId,
  from,
  to,
}: {
  sessionToken: string;
  firmId: string;
  from?: string;
  to?: string;
}) {
  const t = await getTranslations("Financials");
  const report = await fetchPnLReport(sessionToken, firmId, {
    from: startOfDayUTC(from),
    to: endOfDayUTC(to),
  });

  return (
    <div className="flex w-full max-w-4xl flex-col gap-4">
      <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("pnlTitle")}
      </h2>

      {/* A plain GET form - a real navigation, not client-side state, per
          this page's own doc comment above. */}
      <form method="get" className="flex flex-wrap items-end gap-3">
        <input type="hidden" name="tab" value="pnl" />
        <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
          {t("fromLabel")}
          <input
            type="date"
            name="from"
            defaultValue={from ?? ""}
            className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
          {t("toLabel")}
          <input
            type="date"
            name="to"
            defaultValue={to ?? ""}
            className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>
        <button
          type="submit"
          data-permission-public="true"
          className="rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white dark:bg-zinc-50 dark:text-black"
        >
          {t("applyButton")}
        </button>
      </form>

      {report === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <>
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 font-medium" colSpan={3}>
                    {t("pnlRevenueSection")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {report.revenue.length === 0 ? (
                  <tr>
                    <td className="py-2 text-zinc-500 dark:text-zinc-500" colSpan={3}>
                      {t("noAccounts")}
                    </td>
                  </tr>
                ) : (
                  reportLineRows(report.revenue)
                )}
                <tr className="font-medium text-black dark:text-zinc-50">
                  <td className="py-2 pr-4" colSpan={2}>
                    {t("pnlTotalRevenue")}
                  </td>
                  <td className="py-2 font-mono">{report.totalRevenue}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 font-medium" colSpan={3}>
                    {t("pnlExpensesSection")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {report.expenses.length === 0 ? (
                  <tr>
                    <td className="py-2 text-zinc-500 dark:text-zinc-500" colSpan={3}>
                      {t("noAccounts")}
                    </td>
                  </tr>
                ) : (
                  reportLineRows(report.expenses)
                )}
                <tr className="font-medium text-black dark:text-zinc-50">
                  <td className="py-2 pr-4" colSpan={2}>
                    {t("pnlTotalExpenses")}
                  </td>
                  <td className="py-2 font-mono">{report.totalExpenses}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div className="flex items-center justify-between rounded-md border border-zinc-300 p-3 text-sm font-semibold text-black dark:border-zinc-700 dark:text-zinc-50">
            <span>{t("pnlNetIncome")}</span>
            <span className="font-mono">{report.netIncome}</span>
          </div>
        </>
      )}
    </div>
  );
}

async function BalanceSheetTab({
  sessionToken,
  firmId,
  asOf,
}: {
  sessionToken: string;
  firmId: string;
  asOf?: string;
}) {
  const t = await getTranslations("Financials");
  const report = await fetchBalanceSheetReport(sessionToken, firmId, { asOf: endOfDayUTC(asOf) });

  return (
    <div className="flex w-full max-w-4xl flex-col gap-4">
      <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("balanceSheetTitle")}
      </h2>

      <form method="get" className="flex flex-wrap items-end gap-3">
        <input type="hidden" name="tab" value="balance-sheet" />
        <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
          {t("asOfLabel")}
          <input
            type="date"
            name="asOf"
            defaultValue={asOf ?? ""}
            className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>
        <button
          type="submit"
          data-permission-public="true"
          className="rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white dark:bg-zinc-50 dark:text-black"
        >
          {t("applyButton")}
        </button>
      </form>

      {report === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <>
          {!report.balanced && (
            <p className="rounded-md border border-red-400 bg-red-50 p-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300">
              {t("balanceSheetOutOfBalance", { difference: report.difference })}
            </p>
          )}

          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 font-medium" colSpan={3}>
                    {t("balanceSheetAssetsSection")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {report.assets.length === 0 ? (
                  <tr>
                    <td className="py-2 text-zinc-500 dark:text-zinc-500" colSpan={3}>
                      {t("noAccounts")}
                    </td>
                  </tr>
                ) : (
                  reportLineRows(report.assets)
                )}
                <tr className="font-medium text-black dark:text-zinc-50">
                  <td className="py-2 pr-4" colSpan={2}>
                    {t("balanceSheetTotalAssets")}
                  </td>
                  <td className="py-2 font-mono">{report.totalAssets}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 font-medium" colSpan={3}>
                    {t("balanceSheetLiabilitiesSection")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {report.liabilities.length === 0 ? (
                  <tr>
                    <td className="py-2 text-zinc-500 dark:text-zinc-500" colSpan={3}>
                      {t("noAccounts")}
                    </td>
                  </tr>
                ) : (
                  reportLineRows(report.liabilities)
                )}
                <tr className="font-medium text-black dark:text-zinc-50">
                  <td className="py-2 pr-4" colSpan={2}>
                    {t("balanceSheetTotalLiabilities")}
                  </td>
                  <td className="py-2 font-mono">{report.totalLiabilities}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 font-medium" colSpan={3}>
                    {t("balanceSheetEquitySection")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {report.equity.length === 0 ? (
                  <tr>
                    <td className="py-2 text-zinc-500 dark:text-zinc-500" colSpan={3}>
                      {t("noAccounts")}
                    </td>
                  </tr>
                ) : (
                  reportLineRows(report.equity)
                )}
                <tr className="text-black dark:text-zinc-50">
                  <td className="py-2 pr-4" colSpan={2}>
                    {t("balanceSheetCurrentEarnings")}
                  </td>
                  <td className="py-2 font-mono">{report.currentEarnings}</td>
                </tr>
                <tr className="font-medium text-black dark:text-zinc-50">
                  <td className="py-2 pr-4" colSpan={2}>
                    {t("balanceSheetTotalEquity")}
                  </td>
                  <td className="py-2 font-mono">{report.totalEquity}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </>
      )}
    </div>
  );
}

// AgingTab (Invoicing/payment tracking batch): the receivables aging
// report - every unpaid invoice (status 'sent'/'overdue') grouped into
// one of four fixed age buckets by internal/invoicing.ReceivablesAging,
// "needed for any real financial management" (design brief). No date
// filter of its own - unlike PnLTab/BalanceSheetTab, the bucket
// boundaries are fixed and the report is always "as of right now" (see
// ReceivablesAging's own doc comment on why age is computed in SQL
// against CURRENT_DATE, not a caller-supplied instant).
// Cost centers module (internal/costcenter): the cost-center P&L report,
// grouped by cost center (plus the "Unassigned" bucket for journal
// entries with no cost center on them - costCenterId absent/null, see
// lib/costcenter.ts's own doc comment). Same from/to date-filter shape
// as PnLTab above (a real GET navigation, not client-side state).
async function CostCenterPnLTab({
  sessionToken,
  firmId,
  from,
  to,
}: {
  sessionToken: string;
  firmId: string;
  from?: string;
  to?: string;
}) {
  const t = await getTranslations("Financials");
  const lines = await fetchCostCenterPnL(sessionToken, firmId, {
    from: startOfDayUTC(from),
    to: endOfDayUTC(to),
  });

  return (
    <div className="flex w-full max-w-4xl flex-col gap-4">
      <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("costCenterPnlTitle")}
      </h2>

      <form method="get" className="flex flex-wrap items-end gap-3">
        <input type="hidden" name="tab" value="cost-center-pnl" />
        <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
          {t("fromLabel")}
          <input
            type="date"
            name="from"
            defaultValue={from ?? ""}
            className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
          {t("toLabel")}
          <input
            type="date"
            name="to"
            defaultValue={to ?? ""}
            className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
          />
        </label>
        <button
          type="submit"
          data-permission-public="true"
          className="rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white dark:bg-zinc-50 dark:text-black"
        >
          {t("applyButton")}
        </button>
      </form>

      {lines === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : lines.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("noAccounts")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("costCenterPnlName")}</th>
                <th className="py-2 pr-4 font-medium">{t("costCenterPnlRevenue")}</th>
                <th className="py-2 pr-4 font-medium">{t("costCenterPnlExpenses")}</th>
                <th className="py-2 font-medium">{t("costCenterPnlNet")}</th>
              </tr>
            </thead>
            <tbody>
              {lines.map((l) => (
                <tr
                  key={l.costCenterId ?? "unassigned"}
                  className="border-b border-zinc-200 text-black last:border-0 dark:border-zinc-800 dark:text-zinc-50"
                >
                  <td className="py-2 pr-4">
                    {/* l.name is server-set data (internal/costcenter.GetCostCenterPnL
                        literally returns "Unassigned" for the null-cost-center
                        bucket, see COALESCE(cc.name, 'Unassigned') in report.go) -
                        displayed as-is, not routed through next-intl, same
                        treatment as an account code or currency code elsewhere
                        on this page. */}
                    {l.name}
                  </td>
                  <td className="py-2 pr-4 font-mono">{l.revenue}</td>
                  <td className="py-2 pr-4 font-mono">{l.expenses}</td>
                  <td className="py-2 font-mono">{l.net}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

async function AgingTab({ sessionToken, firmId }: { sessionToken: string; firmId: string }) {
  const t = await getTranslations("Financials");
  const buckets = await fetchReceivablesAging(sessionToken, firmId);

  return (
    <div className="flex w-full max-w-4xl flex-col gap-4">
      <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("agingTitle")}</h2>

      {buckets === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("agingBucket")}</th>
                <th className="py-2 pr-4 font-medium">{t("agingCount")}</th>
                <th className="py-2 font-medium">{t("agingOutstanding")}</th>
              </tr>
            </thead>
            <tbody>
              {buckets.map((b) => (
                <tr key={b.label} className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50">
                  <td className="py-2 pr-4 font-mono">{b.label}</td>
                  <td className="py-2 pr-4">{b.count}</td>
                  <td className="py-2 font-mono">{b.outstanding}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
