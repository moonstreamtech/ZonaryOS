// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations } from "next-intl/server";
import type { InstanceState } from "@/lib/workflow";
import type { AuditLogEntry } from "@/lib/auditlog";
import { buildHistoryRows } from "./history";
import { formatPayload } from "./format";

type Props = {
  instances: InstanceState[];
  entries: AuditLogEntry[];
};

// Generic replacement for the old stock-specific stock/StockHistory.tsx.
// Only rendered by its caller when fetchAuditLog actually returned
// entries - a caller without internal/auditlog.ReadPermission (not the
// owner role, by default) simply doesn't get this section.
export default async function WorkflowHistory({ instances, entries }: Props) {
  const t = await getTranslations("Workflow");
  const rows = buildHistoryRows(instances, entries);

  return (
    <div className="w-full">
      <h2 className="mb-3 text-lg font-semibold text-black dark:text-zinc-50">
        {t("historyTitle")}
      </h2>

      {rows.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("historyEmpty")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("historyColumnWhen")}</th>
                <th className="py-2 pr-4 font-medium">{t("historyColumnWho")}</th>
                <th className="py-2 pr-4 font-medium">{t("historyColumnAction")}</th>
                <th className="py-2 font-medium">{t("historyColumnPayload")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr
                  key={row.id}
                  className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50"
                >
                  <td className="py-2 pr-4 whitespace-nowrap">
                    {new Date(row.occurredAt).toLocaleString()}
                  </td>
                  <td className="py-2 pr-4">
                    {row.actorDisplayName || row.actorEmail}
                  </td>
                  {/* row.action/toStateKey are workflow_transitions.action_key
                      / workflow_states.key values from the backend - data,
                      not UI copy, same convention the original
                      StockList.tsx documented for workflow state/action
                      names. */}
                  <td className="py-2 pr-4">
                    {row.action}
                    {row.toStateKey ? ` → ${row.toStateKey}` : ""}
                  </td>
                  <td className="py-2">
                    {formatPayload(row.payload) || (
                      <span className="text-zinc-400 dark:text-zinc-600">
                        —
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
