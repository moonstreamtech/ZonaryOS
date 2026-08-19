// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchTopByProperty, fetchActiveUserCount } from "@/lib/analytics";
import { formatNumber } from "@/lib/format";
import { fetchFirmMetadata } from "@/lib/firm";
import RankedList from "@/components/Analytics/RankedList";
import TrendChart from "@/components/Analytics/TrendChart";

type PageProps = {
  params: Promise<{ locale: string }>;
};

const ACTIVE_USER_DAYS = 30;
const TOP_LIMIT = 10;
const HEATMAP_LIMIT = 20;

// Analytics/BI/advanced reporting batch, Part A: /analytics. Member-
// visible (the underlying internal/analytics reads are member-gated, not
// owner-gated - see internal/analytics/handlers.go's own RegisterRoutes
// doc comment), unlike /settings/webhooks. Same
// requireFirmContext()-then-fetch-server-side shape as /reports's own
// DashboardTab; only the feature-usage trend chart (event_type is
// user-selectable) needs client-side fetching, see
// components/Analytics/TrendChart.tsx.
export default async function AnalyticsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Analytics");

  const { sessionToken, firm } = await requireFirmContext(locale);

  const [topWorkflows, pageViews, activeUsers, metadata] = await Promise.all([
    fetchTopByProperty(sessionToken, firm.firmId, "workflow_executed", "definitionKey", TOP_LIMIT),
    fetchTopByProperty(sessionToken, firm.firmId, "page_view", "page", HEATMAP_LIMIT),
    fetchActiveUserCount(sessionToken, firm.firmId, ACTIVE_USER_DAYS),
    fetchFirmMetadata(sessionToken, firm.firmId),
  ]);
  const formatLocale = metadata?.defaultLocale || locale;

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
        <p className="max-w-2xl text-center text-sm text-zinc-600 dark:text-zinc-400">{t("description")}</p>
      </div>

      <div className="grid w-full max-w-4xl grid-cols-1 gap-4 sm:grid-cols-3">
        <div className="flex flex-col gap-1 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950 sm:col-span-1">
          <span className="text-xs font-medium text-zinc-600 dark:text-zinc-400">
            {t("activeUsersLabel", { days: ACTIVE_USER_DAYS })}
          </span>
          <span className="text-2xl font-semibold text-black dark:text-zinc-50">
            {activeUsers === null ? t("loadError") : formatNumber(activeUsers.count, formatLocale)}
          </span>
        </div>
      </div>

      <div className="w-full max-w-4xl">
        <TrendChart firmId={firm.firmId} locale={formatLocale} />
      </div>

      <div className="grid w-full max-w-4xl grid-cols-1 gap-4 lg:grid-cols-2">
        <div className="flex flex-col gap-3 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950">
          <h3 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("topWorkflowsTitle")}</h3>
          {topWorkflows === null ? (
            <p className="text-sm text-red-600 dark:text-red-400">{t("loadError")}</p>
          ) : (
            <RankedList
              items={topWorkflows}
              valueLabel={t("workflowColumn")}
              countLabel={t("executionsColumn")}
              emptyLabel={t("topWorkflowsEmpty")}
            />
          )}
        </div>

        <div className="flex flex-col gap-3 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950">
          <h3 className="text-lg font-semibold tracking-tight text-black dark:text-zinc-50">{t("pageViewsTitle")}</h3>
          {pageViews === null ? (
            <p className="text-sm text-red-600 dark:text-red-400">{t("loadError")}</p>
          ) : (
            <RankedList
              items={pageViews}
              valueLabel={t("pageColumn")}
              countLabel={t("viewsColumn")}
              emptyLabel={t("pageViewsEmpty")}
            />
          )}
        </div>
      </div>
    </main>
  );
}
