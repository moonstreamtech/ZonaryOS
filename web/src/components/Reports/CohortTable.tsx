// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations } from "next-intl/server";
import type { CohortTable as CohortTableData } from "@/lib/reports";
import { formatCurrency } from "@/lib/format";

type Props = {
  table: CohortTableData;
  currency: string;
  locale: string;
};

// CSS-only conditional-formatting bucket count (design brief: "4-5
// buckets of opacity/shade") - no charting/heatmap JS library, just a
// small Tailwind class lookup keyed by intensity computed client-...
// actually server-side, since this whole table is a pure server
// component (the intensity only depends on props already fetched).
const INTENSITY_CLASSES = [
  "bg-transparent",
  "bg-emerald-100 dark:bg-emerald-950",
  "bg-emerald-200 dark:bg-emerald-900",
  "bg-emerald-300 dark:bg-emerald-800",
  "bg-emerald-400 dark:bg-emerald-700",
];

function intensityClass(value: number | null, max: number): string {
  if (value === null || max <= 0) return INTENSITY_CLASSES[0];
  const ratio = value / max;
  const bucket = Math.min(INTENSITY_CLASSES.length - 1, Math.max(1, Math.ceil(ratio * (INTENSITY_CLASSES.length - 1))));
  return INTENSITY_CLASSES[bucket];
}

// The /reports "Cohort Analysis" tab's table: rows = cohorts (labeled by
// month + size), columns = periods 0..N-1. A null value ("no data for
// that period yet", per internal/reports/cohort.go's own CohortRow.Values
// doc comment) renders as a blank em-dash cell, never "0" and never
// colored - it is not the same thing as zero revenue.
export default async function CohortTable({ table, currency, locale }: Props) {
  const t = await getTranslations("Reports");
  const max = table.cohorts.reduce((m, row) => {
    const rowMax = row.values.reduce<number>((rm, v) => (v !== null && v > rm ? v : rm), 0);
    return Math.max(m, rowMax);
  }, 0);

  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-left text-sm">
        <thead>
          <tr className="border-b border-zinc-200 text-zinc-600 dark:border-zinc-800 dark:text-zinc-400">
            <th className="py-1.5 pr-3 font-medium">{t("cohortColumn")}</th>
            {Array.from({ length: table.periods }, (_, p) => (
              <th key={p} className="py-1.5 px-3 font-medium">
                {t("cohortPeriodLabel", { period: p })}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {table.cohorts.map((row) => (
            <tr key={row.cohortMonth} className="border-b border-zinc-100 text-black dark:border-zinc-900 dark:text-zinc-50">
              <td className="whitespace-nowrap py-1.5 pr-3 font-medium">
                {row.cohortMonth} ({t("cohortSizeLabel", { count: row.cohortSize })})
              </td>
              {row.values.map((value, idx) => (
                <td key={idx} className={`py-1.5 px-3 text-right tabular-nums ${intensityClass(value, max)}`}>
                  {value === null ? "—" : formatCurrency(value, currency, locale)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
