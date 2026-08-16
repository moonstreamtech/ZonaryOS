// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { Link } from "@/i18n/navigation";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchDashboardKPIs, fetchReportDefinitions } from "@/lib/reports";
import MyReportsPanel from "@/components/Reports/MyReportsPanel";

type PageProps = {
  params: Promise<{ locale: string }>;
  searchParams: Promise<{ tab?: string }>;
};

type Tab = "dashboard" | "my-reports";

// Vision §3's reporting foundation: a fixed KPI dashboard (unchanged) plus
// this batch's parametric reporting engine - firms define their own
// reports as query_spec descriptors (internal/reports.QuerySpec), not
// SQL, via /reports/builder; this page's "My Reports" tab lists and runs
// them. Same URL-driven tab convention as /financials's own TabLink.
export default async function ReportsPage({ params, searchParams }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Reports");
  const sp = await searchParams;
  const tab: Tab = sp.tab === "my-reports" ? "my-reports" : "dashboard";

  const { sessionToken, firm } = await requireFirmContext(locale);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-4">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("title")}
        </h1>
        <nav className="flex gap-2 text-sm">
          <TabLink locale={locale} tab="dashboard" active={tab === "dashboard"} label={t("tabDashboard")} />
          <TabLink locale={locale} tab="my-reports" active={tab === "my-reports"} label={t("tabMyReports")} />
        </nav>
      </div>

      {tab === "dashboard" ? (
        <DashboardTab sessionToken={sessionToken} firmId={firm.firmId} />
      ) : (
        <MyReportsTab sessionToken={sessionToken} firmId={firm.firmId} locale={locale} />
      )}
    </main>
  );
}

function TabLink({ locale, tab, active, label }: { locale: string; tab: Tab; active: boolean; label: string }) {
  return (
    <a
      href={`/${locale}/reports?tab=${tab}`}
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

async function DashboardTab({ sessionToken, firmId }: { sessionToken: string; firmId: string }) {
  const t = await getTranslations("Reports");
  const kpis = await fetchDashboardKPIs(sessionToken, firmId);

  return kpis === null ? (
    <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
  ) : (
    <div className="grid w-full max-w-4xl grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {kpis.map((kpi) => (
        <div
          key={kpi.key}
          className="flex flex-col gap-1 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950"
        >
          <span className="text-xs font-medium text-zinc-600 dark:text-zinc-400">
            {t(`kpi.${kpi.key}`)}
          </span>
          <span className="text-2xl font-semibold text-black dark:text-zinc-50">
            {kpi.unit === "currency" ? `${kpi.value} TRY` : kpi.value}
          </span>
          {/* overdueTasks is a heuristic (task_approval instances open
              >30 days), not a real due-date check - this caveat has to
              be visible on the tile itself, not just implied by the
              label, per the batch brief. */}
          {kpi.key === "overdueTasks" && (
            <span className="text-xs text-zinc-500 dark:text-zinc-500">{t("overdueTasksCaveat")}</span>
          )}
        </div>
      ))}
    </div>
  );
}

async function MyReportsTab({
  sessionToken,
  firmId,
  locale,
}: {
  sessionToken: string;
  firmId: string;
  locale: string;
}) {
  const t = await getTranslations("Reports");
  const definitions = await fetchReportDefinitions(sessionToken, firmId);

  return (
    <div className="flex w-full max-w-4xl flex-col gap-4">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("myReportsTitle")}</h2>
        <Link
          href="/reports/builder"
          data-permission-public="true"
          className="rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white dark:bg-zinc-50 dark:text-black"
        >
          {t("newReport")}
        </Link>
      </div>

      {definitions === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <MyReportsPanel firmId={firmId} definitions={definitions} locale={locale} />
      )}
    </div>
  );
}
