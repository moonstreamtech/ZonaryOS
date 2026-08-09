"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useTranslations } from "next-intl";
import type { ReportDefinition, ReportRow } from "@/lib/reports";

type Props = {
  firmId: string;
  definitions: ReportDefinition[];
  locale: string;
};

// The /reports "My Reports" tab's client half: lists saved
// report_definitions and runs one synchronously against the Next proxy
// route, which forwards to internal/reports.RunReport - results render
// inline as a table (group key + each requested metric), same "no
// separate results page" scope internal/reports' own design brief calls
// for ("runs synchronously ... result directly in response").
export default function MyReportsPanel({ firmId, definitions }: Props) {
  const t = useTranslations("Reports");

  const [runningId, setRunningId] = useState<string | null>(null);
  const [rowsByDefinition, setRowsByDefinition] = useState<Record<string, ReportRow[]>>({});
  const [errorByDefinition, setErrorByDefinition] = useState<Record<string, string>>({});

  async function runDefinition(definitionId: string) {
    setRunningId(definitionId);
    setErrorByDefinition((prev) => ({ ...prev, [definitionId]: "" }));
    try {
      const res = await fetch(`/api/reports/definitions/${firmId}/${definitionId}/run`, { method: "POST" });
      if (!res.ok) {
        setErrorByDefinition((prev) => ({ ...prev, [definitionId]: t("runError") }));
        return;
      }
      const rows = (await res.json()) as ReportRow[];
      setRowsByDefinition((prev) => ({ ...prev, [definitionId]: rows }));
    } catch {
      setErrorByDefinition((prev) => ({ ...prev, [definitionId]: t("runError") }));
    } finally {
      setRunningId(null);
    }
  }

  if (definitions.length === 0) {
    return <p className="text-sm text-zinc-600 dark:text-zinc-400">{t("myReportsEmpty")}</p>;
  }

  return (
    <div className="flex flex-col gap-4">
      {definitions.map((def) => {
        const rows = rowsByDefinition[def.id];
        const rowError = errorByDefinition[def.id];
        return (
          <div key={def.id} className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <h3 className="text-base font-semibold text-black dark:text-zinc-50">{def.name}</h3>
                {def.description && (
                  <p className="text-xs text-zinc-600 dark:text-zinc-400">{def.description}</p>
                )}
              </div>
              <button
                type="button"
                data-permission-public="true"
                disabled={runningId === def.id}
                onClick={() => runDefinition(def.id)}
                className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50"
              >
                {runningId === def.id ? t("running") : t("runReport")}
              </button>
            </div>

            {rowError && <p className="text-xs text-red-600 dark:text-red-400">{rowError}</p>}

            {rows && (
              <div className="overflow-x-auto">
                <table className="w-full border-collapse text-left text-sm">
                  <thead>
                    <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                      {def.querySpec.group_by && <th className="py-2 pr-4 font-medium">{t("groupBy")}</th>}
                      {def.querySpec.metrics.map((m) => (
                        <th key={m.name} className="py-2 pr-4 font-medium">
                          {m.name}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {rows.length === 0 ? (
                      <tr>
                        <td className="py-2 text-zinc-500 dark:text-zinc-500" colSpan={def.querySpec.metrics.length + 1}>
                          {t("noResults")}
                        </td>
                      </tr>
                    ) : (
                      rows.map((row, idx) => (
                        <tr key={idx} className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50">
                          {def.querySpec.group_by && <td className="py-2 pr-4 font-mono">{row.groupKey ?? "—"}</td>}
                          {def.querySpec.metrics.map((m) => (
                            <td key={m.name} className="py-2 pr-4 font-mono">
                              {String(row.metrics[m.name] ?? "")}
                            </td>
                          ))}
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
