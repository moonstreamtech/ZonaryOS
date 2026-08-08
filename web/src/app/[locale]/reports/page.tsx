// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchDashboardKPIs } from "@/lib/reports";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Vision §3's reporting foundation: a KPI dashboard over data that
// already exists (internal/reports.GetDashboardKPIs, a cross-module read
// spanning accounting/workflow/hr) - no new data entry on this page,
// pure aggregation display. Member-gated, same tier as /financials.
export default async function ReportsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Reports");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const kpis = await fetchDashboardKPIs(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("title")}
      </h1>

      {kpis === null ? (
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
            </div>
          ))}
        </div>
      )}
    </main>
  );
}
