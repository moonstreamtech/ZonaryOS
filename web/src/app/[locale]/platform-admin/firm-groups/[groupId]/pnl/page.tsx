// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { notFound } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchPlatformAdminFirms } from "@/lib/platformAdmin";
import { fetchFirmGroups, fetchConsolidatedPnL } from "@/lib/consolidation";

type PageProps = {
  params: Promise<{ locale: string; groupId: string }>;
  searchParams: Promise<{ from?: string; to?: string }>;
};

// The date `<input type="date">` fields below submit a plain "YYYY-MM-DD"
// via a real GET navigation (same convention financials/page.tsx's own
// PnLTab uses), but the backend's ?from=/?to= parse RFC3339 timestamps
// (internal/consolidation/handlers.go's parseDateRange) - these two
// helpers bridge that gap right before the fetch call, same
// start-of-day/end-of-day UTC conversion financials/page.tsx already
// does for its own P&L tab (duplicated here rather than shared, matching
// that file's own choice not to export them).
function startOfDayUTC(dateOnly?: string): string | undefined {
  if (!dateOnly) return undefined;
  return `${dateOnly}T00:00:00Z`;
}
function endOfDayUTC(dateOnly?: string): string | undefined {
  if (!dateOnly) return undefined;
  return `${dateOnly}T23:59:59Z`;
}

// Group consolidated P&L view (internal/consolidation.GetConsolidatedPnL)
// - platform-admin-gated exactly like /platform-admin/firm-groups itself
// (notFound(), not an inline "not authorized" message). The from/to date
// filter is a plain GET navigation, not client-side state, matching
// /financials's own P&L tab shape (see that page's doc comment on why).
export default async function GroupConsolidatedPnLPage({ params, searchParams }: PageProps) {
  const { locale, groupId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("FirmGroups");
  const tAdmin = await getTranslations("PlatformAdmin");
  const sp = await searchParams;

  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  if (!sessionToken) {
    notFound();
  }

  const allowlistProbe = await fetchPlatformAdminFirms(sessionToken);
  if (allowlistProbe === null) {
    notFound();
  }

  const [groups, report] = await Promise.all([
    fetchFirmGroups(sessionToken),
    fetchConsolidatedPnL(sessionToken, groupId, {
      from: startOfDayUTC(sp.from),
      to: endOfDayUTC(sp.to),
    }),
  ]);
  const group = groups?.find((g) => g.id === groupId);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-[var(--color-platform-accent)] px-6 py-16">
      <div className="flex w-full max-w-4xl flex-col items-center gap-3">
        <span className="rounded-full bg-[var(--color-platform-accent-strong)] px-3 py-1 text-xs font-semibold tracking-wide text-[var(--color-platform-on-accent)] uppercase">
          {tAdmin("badge")}
        </span>
        <h1 className="text-3xl font-semibold tracking-tight text-[var(--color-platform-on-accent)]">
          {t("pnlTitle", { groupName: group?.name ?? groupId })}
        </h1>
        <a
          href={`/${locale}/platform-admin/firm-groups`}
          data-permission-public="true"
          className="text-sm text-indigo-300 hover:underline"
        >
          {t("backToGroups")}
        </a>
      </div>

      <div className="w-full max-w-4xl rounded-lg border border-indigo-800 bg-white p-4 dark:bg-zinc-950">
        {/* A plain GET form - a real navigation, matching
            financials/page.tsx's own PnLTab. */}
        <form method="get" className="flex flex-wrap items-end gap-3">
          <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
            {t("fromLabel")}
            <input
              type="date"
              name="from"
              defaultValue={sp.from ?? ""}
              className="rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-zinc-600 dark:text-zinc-400">
            {t("toLabel")}
            <input
              type="date"
              name="to"
              defaultValue={sp.to ?? ""}
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
          <p className="mt-4 text-red-600 dark:text-red-400">{t("loadError")}</p>
        ) : (
          <div className="mt-4 overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 font-medium">{t("columnFirm")}</th>
                  <th className="py-2 pr-4 font-medium">{t("pnlColumnRevenue")}</th>
                  <th className="py-2 pr-4 font-medium">{t("pnlColumnExpenses")}</th>
                  <th className="py-2 font-medium">{t("pnlColumnNetIncome")}</th>
                </tr>
              </thead>
              <tbody>
                {report.firms.length === 0 ? (
                  <tr>
                    <td className="py-2 text-zinc-500 dark:text-zinc-500" colSpan={4}>
                      {t("noMembers")}
                    </td>
                  </tr>
                ) : (
                  report.firms.map((f) => (
                    <tr
                      key={f.firmId}
                      className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50"
                    >
                      <td className="py-2 pr-4">{f.firmName}</td>
                      <td className="py-2 pr-4 font-mono">{f.revenue}</td>
                      <td className="py-2 pr-4 font-mono">{f.expenses}</td>
                      <td className="py-2 font-mono">{f.netIncome}</td>
                    </tr>
                  ))
                )}
                <tr className="font-semibold text-black dark:text-zinc-50">
                  <td className="py-2 pr-4">{t("pnlTotalsLabel")}</td>
                  <td className="py-2 pr-4 font-mono">{report.totalRevenue}</td>
                  <td className="py-2 pr-4 font-mono">{report.totalExpenses}</td>
                  <td className="py-2 font-mono">{report.netIncome}</td>
                </tr>
              </tbody>
            </table>
          </div>
        )}
      </div>
    </main>
  );
}
