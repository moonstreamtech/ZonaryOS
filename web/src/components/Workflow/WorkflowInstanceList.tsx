"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { InstanceState } from "@/lib/workflow";
import { formatPayload } from "./format";

type Props = {
  firmId: string;
  instances: InstanceState[];
};

// Generic replacement for the old stock-specific StockList.tsx: renders
// any workflow definition's instances, driven entirely by what the
// backend returns for each one (state.name, payload, and each
// AvailableAction's own name/actionKey/permissionKey) rather than
// hardcoding "Sell"/"Add Stock" as literal button labels or "Item"/
// "Quantity" as literal columns. A second, structurally different
// workflow definition renders correctly here with zero changes to this
// file - see internal/workflow/workflow_integration_test.go's
// purchaseOrderSpec and this component's own instanceList.test.ts for
// the proof.
export default function WorkflowInstanceList({ firmId, instances }: Props) {
  const t = useTranslations("Workflow");
  const router = useRouter();
  const [pendingKey, setPendingKey] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function runAction(instanceId: string, actionKey: string) {
    setError(null);
    const pendingId = `${instanceId}:${actionKey}`;
    setPendingKey(pendingId);
    try {
      const res = await fetch(
        `/api/workflow/instances/${instanceId}/transitions/${actionKey}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ firmId }),
        },
      );
      if (!res.ok) {
        setError(
          res.status === 403 ? t("actionPermissionDenied") : t("actionError"),
        );
        return;
      }
      router.refresh();
    } catch {
      setError(t("actionError"));
    } finally {
      setPendingKey(null);
    }
  }

  return (
    <div className="flex w-full flex-col items-center gap-4">
      {error && <p className="text-red-600 dark:text-red-400">{error}</p>}

      {instances.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="w-full overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("columnPayload")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnState")}</th>
                <th className="py-2 font-medium">{t("columnActions")}</th>
              </tr>
            </thead>
            <tbody>
              {instances.map((instance) => (
                <tr
                  key={instance.instanceId}
                  className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50"
                >
                  <td className="py-2 pr-4">
                    {formatPayload(instance.payload) || (
                      <span className="text-zinc-400 dark:text-zinc-600">
                        —
                      </span>
                    )}
                  </td>
                  {/* state.name is workflow_states.name from the backend
                      - data, not UI copy, same convention this codebase
                      has used since the original StockList.tsx for
                      workflow state/action names. */}
                  <td className="py-2 pr-4">{instance.state.name}</td>
                  <td className="py-2">
                    {instance.availableActions.length === 0 ? (
                      <span className="text-zinc-400 dark:text-zinc-600">
                        —
                      </span>
                    ) : (
                      <div className="flex flex-wrap gap-2">
                        {instance.availableActions.map((action) => {
                          const pendingId = `${instance.instanceId}:${action.actionKey}`;
                          return (
                            <button
                              key={action.actionKey}
                              type="button"
                              data-permission-key={action.permissionKey}
                              disabled={pendingKey === pendingId}
                              onClick={() =>
                                runAction(instance.instanceId, action.actionKey)
                              }
                              className="rounded-full bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] disabled:opacity-50 dark:hover:bg-[#ccc]"
                            >
                              {action.name}
                            </button>
                          );
                        })}
                      </div>
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
