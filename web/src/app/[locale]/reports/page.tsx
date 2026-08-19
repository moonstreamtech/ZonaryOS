// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { Link } from "@/i18n/navigation";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchDashboardKPIs, fetchReportDefinitions, fetchCohortAnalysis } from "@/lib/reports";
import { fetchFirmMetadata } from "@/lib/firm";
import { formatCurrency, formatNumber } from "@/lib/format";
import MyReportsPanel from "@/components/Reports/MyReportsPanel";
import CohortTable from "@/components/Reports/CohortTable";

type PageProps = {
  params: Promise<{ locale: string }>;
  searchParams: Promise<{ tab?: string }>;
};

type Tab = "dashboard" | "my-reports" | "cohort-analysis";

const COHORT_PERIODS = 6;

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
  const tab: Tab = sp.tab === "my-reports" ? "my-reports" : sp.tab === "cohort-analysis" ? "cohort-analysis" : "dashboard";

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
          <TabLink locale={locale} tab="cohort-analysis" active={tab === "cohort-analysis"} label={t("tabCohortAnalysis")} />
        </nav>
      </div>

      {tab === "dashboard" ? (
        <DashboardTab sessionToken={sessionToken} firmId={firm.firmId} locale={locale} />
      ) : tab === "my-reports" ? (
        <MyReportsTab sessionToken={sessionToken} firmId={firm.firmId} locale={locale} />
      ) : (
        <CohortAnalysisTab sessionToken={sessionToken} firmId={firm.firmId} locale={locale} />
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

async function DashboardTab({
  sessionToken,
  firmId,
  locale,
}: {
  sessionToken: string;
  firmId: string;
  locale: string;
}) {
  const t = await getTranslations("Reports");
  const [kpis, metadata] = await Promise.all([
    fetchDashboardKPIs(sessionToken, firmId),
    fetchFirmMetadata(sessionToken, firmId),
  ]);
  const formatLocale = metadata?.defaultLocale || locale;
  // "TRY" preserves this tile's previous hardcoded currency when the
  // firm hasn't set its own default_currency - see format.ts's own doc
  // comment on why the fallback choice is each call site's, not the
  // formatter's.
  const currency = metadata?.defaultCurrency || "TRY";

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
            {kpi.unit === "currency"
              ? formatCurrency(Number(kpi.value), currency, formatLocale)
              : formatNumber(Number(kpi.value), formatLocale)}
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

// Part B of the analytics/BI/advanced reporting batch: the Cohort
// Analysis tab (internal/reports/cohort.go). Only the one combination
// that endpoint supports today (customers/created_at/invoice_total) - a
// classic SaaS retention table.
async function CohortAnalysisTab({
  sessionToken,
  firmId,
  locale,
}: {
  sessionToken: string;
  firmId: string;
  locale: string;
}) {
  const t = await getTranslations("Reports");
  const [table, metadata] = await Promise.all([
    fetchCohortAnalysis(sessionToken, firmId, {
      entity: "customers",
      cohortBy: "created_at",
      metric: "invoice_total",
      periods: COHORT_PERIODS,
    }),
    fetchFirmMetadata(sessionToken, firmId),
  ]);
  const formatLocale = metadata?.defaultLocale || locale;
  const currency = metadata?.defaultCurrency || "TRY";

  return (
    <div className="flex w-full max-w-4xl flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("cohortTitle")}</h2>
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("cohortDescription")}</p>
      </div>

      {table === null ? (
        <p className="text-red-600 dark:text-red-400">{t("cohortLoadError")}</p>
      ) : table.cohorts.length === 0 ? (
        <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("cohortEmpty")}</p>
      ) : (
        <CohortTable table={table} currency={currency} locale={formatLocale} />
      )}
    </div>
  );
}
