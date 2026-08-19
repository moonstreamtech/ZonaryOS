// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import type { PropertyCount } from "@/lib/analytics";

type Props = {
  items: PropertyCount[];
  valueLabel: string;
  countLabel: string;
  emptyLabel: string;
};

// Shared "ranked list/table" presentational shell behind both "top
// workflows by execution count" and the page-view heatmap table (design
// brief: "literally the same TopByProperty endpoint as #2 with different
// params, so this can likely reuse the same small ranked list
// component"). Pure server component - the data is already fetched by
// the caller, no client interactivity needed here.
export default function RankedList({ items, valueLabel, countLabel, emptyLabel }: Props) {
  if (items.length === 0) {
    return <p className="text-sm text-zinc-600 dark:text-zinc-400">{emptyLabel}</p>;
  }

  const max = Math.max(...items.map((i) => i.count), 1);

  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-left text-sm">
        <thead>
          <tr className="border-b border-zinc-200 text-zinc-600 dark:border-zinc-800 dark:text-zinc-400">
            <th className="py-1.5 pr-3 font-medium">#</th>
            <th className="py-1.5 pr-3 font-medium">{valueLabel}</th>
            <th className="py-1.5 font-medium">{countLabel}</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item, idx) => (
            <tr key={`${item.value}-${idx}`} className="border-b border-zinc-100 text-black dark:border-zinc-900 dark:text-zinc-50">
              <td className="py-1.5 pr-3 text-zinc-500 dark:text-zinc-500">{idx + 1}</td>
              <td className="py-1.5 pr-3 font-mono">{item.value || "—"}</td>
              <td className="py-1.5">
                <div className="flex items-center gap-2">
                  <span className="tabular-nums">{item.count}</span>
                  <div className="h-1.5 flex-1 min-w-8 rounded-full bg-zinc-100 dark:bg-zinc-900">
                    <div
                      className="h-1.5 rounded-full bg-black dark:bg-zinc-50"
                      style={{ width: `${Math.max((item.count / max) * 100, 4)}%` }}
                    />
                  </div>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
