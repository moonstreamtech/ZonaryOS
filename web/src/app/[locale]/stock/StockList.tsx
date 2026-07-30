"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).


import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { InstanceState } from "@/lib/workflow";

type Props = {
  firmId: string;
  instances: InstanceState[];
};

const RECORD_SALE_ACTION_KEY = "record_sale";

// state.name/action.name below come straight from the backend
// (workflow_states.name / workflow_transitions.name - see
// internal/workflow/stock_to_sale.go's spec) and are rendered as-is, the
// same way the wizard renders a firm's role name as-is: they're workflow
// data, not UI copy baked into this component, so they're outside the
// i18n namespace below (CLAUDE.md Never-Violate Rule 4 covers this
// component's own chrome - headers, button label, messages - all of
// which do go through "Stock" translations).
export default function StockList({ firmId, instances }: Props) {
  const t = useTranslations("Stock");
  const router = useRouter();
  const [pendingId, setPendingId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function sell(instanceId: string) {
    setError(null);
    setPendingId(instanceId);
    try {
      const res = await fetch(`/api/stock/${instanceId}/sell`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ firmId }),
      });
      if (!res.ok) {
        setError(
          res.status === 403 ? t("sellPermissionDenied") : t("sellError"),
        );
        return;
      }
      router.refresh();
    } catch {
      setError(t("sellError"));
    } finally {
      setPendingId(null);
    }
  }

  return (
    <div className="flex w-full flex-col items-center gap-4">
      {error && <p className="text-red-600 dark:text-red-400">{error}</p>}

      {instances.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="w-full max-w-2xl overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 font-medium">{t("columnItem")}</th>
                <th className="py-2 pr-4 font-medium">
                  {t("columnQuantity")}
                </th>
                <th className="py-2 pr-4 font-medium">{t("columnStatus")}</th>
                <th className="py-2 font-medium">{t("columnAction")}</th>
              </tr>
            </thead>
            <tbody>
              {instances.map((instance) => {
                const sellAction = instance.availableActions.find(
                  (a) => a.actionKey === RECORD_SALE_ACTION_KEY,
                );
                const itemName =
                  typeof instance.payload.item === "string"
                    ? instance.payload.item
                    : t("unknownItem");
                const quantity =
                  typeof instance.payload.quantity === "number"
                    ? instance.payload.quantity
                    : "—";

                return (
                  <tr
                    key={instance.instanceId}
                    className="border-b border-zinc-200 text-black dark:border-zinc-800 dark:text-zinc-50"
                  >
                    <td className="py-2 pr-4">{itemName}</td>
                    <td className="py-2 pr-4">{quantity}</td>
                    <td className="py-2 pr-4">{instance.state.name}</td>
                    <td className="py-2">
                      {sellAction ? (
                        <button
                          type="button"
                          data-permission-key={sellAction.permissionKey}
                          disabled={pendingId === instance.instanceId}
                          onClick={() => sell(instance.instanceId)}
                          className="rounded-full bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] disabled:opacity-50 dark:hover:bg-[#ccc]"
                        >
                          {t("sellButton")}
                        </button>
                      ) : (
                        <span className="text-zinc-400 dark:text-zinc-600">
                          —
                        </span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
