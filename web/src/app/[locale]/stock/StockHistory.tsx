// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations } from "next-intl/server";
import type { InstanceState } from "@/lib/workflow";
import type { AuditLogEntry } from "@/lib/auditlog";
import { buildHistoryRows } from "./history";

type Props = {
  instances: InstanceState[];
  entries: AuditLogEntry[];
};

// Item 6: a view of past transitions (sold items, when, by whom), built
// from the audit log (see history.ts's buildHistoryRows) rather than a
// parallel history mechanism. Only rendered by stock/page.tsx when
// fetchAuditLog actually returned entries - a caller without
// internal/auditlog.ReadPermission (not the owner role, by default -
// see internal/auditlog/auditlog.go) simply doesn't get this section,
// same "omit, don't error" convention the Audit Mode toggle uses.
export default async function StockHistory({ instances, entries }: Props) {
  const t = await getTranslations("Stock");
  const rows = buildHistoryRows(instances, entries);

  return (
    <div className="w-full max-w-2xl">
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
                <th className="py-2 pr-4 font-medium">{t("historyColumnItem")}</th>
                <th className="py-2 font-medium">{t("historyColumnAction")}</th>
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
                  <td className="py-2 pr-4">
                    {row.itemName ?? t("unknownItem")}
                  </td>
                  {/* row.action/toStateKey are workflow_transitions.action_key
                      / workflow_states.key values from the backend, same
                      "data, not UI copy" convention StockList.tsx's own
                      state.name rendering documents - out of the i18n
                      layer on purpose. */}
                  <td className="py-2">
                    {row.action}
                    {row.toStateKey ? ` → ${row.toStateKey}` : ""}
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
