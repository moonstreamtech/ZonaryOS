// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchAccounts, fetchAccountBalance, fetchJournalEntries } from "@/lib/accounting";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Vision §3's financial management core: a basic financials page - each
// account's current balance (computed server-side from
// internal/accounting.GetAccountBalance, itself SUM(debits)-SUM(credits)
// or the reverse depending on the account's normal balance side) and a
// recent-entries list. Member-gated (not owner-gated): reading the
// ledger is ordinary firm data visibility, the same tier as the
// /workflows pages - only chart-of-accounts management
// (/settings/accounts) and posting a manual entry are owner-only. No
// P&L/balance-sheet report yet - this batch's explicit scope boundary,
// the natural next step once this lands.
export default async function FinancialsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Financials");

  const { sessionToken, firm } = await requireFirmContext(locale);

  const [accounts, entriesPage] = await Promise.all([
    fetchAccounts(sessionToken, firm.firmId),
    fetchJournalEntries(sessionToken, firm.firmId, { limit: 25 }),
  ]);

  const balances =
    accounts !== null
      ? await Promise.all(
          accounts.map(async (a) => ({
            account: a,
            balance: await fetchAccountBalance(sessionToken, firm.firmId, a.id),
          })),
        )
      : null;

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("title")}
        </h1>
      </div>

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
                    {new Date(entry.postedAt).toLocaleString(locale)}
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
    </main>
  );
}
