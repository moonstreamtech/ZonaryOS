"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { SalesOrder, SalesOrderStatus } from "@/lib/salesorders";

type Props = {
  firmId: string;
  order: SalesOrder;
  isOwner: boolean;
};

// Manual next-status options per current status - mirrors
// internal/salesorders.allowedStatusTransitions (Go is the actual
// authority; this is just what the button row offers, same "server
// re-validates regardless" convention InvoiceDetail's own
// MANUAL_STATUS_OPTIONS already establishes).
const MANUAL_STATUS_OPTIONS: Record<SalesOrderStatus, SalesOrderStatus[]> = {
  draft: ["confirmed", "cancelled"],
  confirmed: ["picking", "cancelled"],
  picking: ["shipped", "cancelled"],
  shipped: ["delivered", "cancelled"],
  delivered: [],
  cancelled: [],
};

export default function SalesOrderDetail({ firmId, order, isOwner }: Props) {
  const t = useTranslations("SalesOrders");
  const router = useRouter();
  const [changingStatus, setChangingStatus] = useState(false);
  const [statusError, setStatusError] = useState<string | null>(null);

  async function changeStatus(status: SalesOrderStatus) {
    setStatusError(null);
    setChangingStatus(true);
    try {
      const res = await fetch(`/api/salesorders/${firmId}/${order.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status }),
      });
      if (!res.ok) {
        setStatusError(t("statusChangeError"));
        return;
      }
      router.refresh();
    } catch {
      setStatusError(t("statusChangeError"));
    } finally {
      setChangingStatus(false);
    }
  }

  const manualOptions = MANUAL_STATUS_OPTIONS[order.status];

  return (
    <div className="flex w-full max-w-2xl flex-col gap-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {order.orderNumber}
        </h1>
        <span className="text-sm text-zinc-600 dark:text-zinc-400">{t(`status.${order.status}`)}</span>
      </div>

      {order.sourceWorkflowInstance && (
        <p className="text-xs text-zinc-500 dark:text-zinc-500">{t("autoCreatedNote")}</p>
      )}

      <div className="panel flex flex-col gap-2 p-4 text-sm">
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("columnTotal")}</span>
          <span className="font-mono">
            {order.total} {order.currency}
          </span>
        </div>
        {order.shippingAddress && (
          <div className="flex justify-between">
            <span className="text-zinc-600 dark:text-zinc-400">{t("shippingAddress")}</span>
            <span>{order.shippingAddress}</span>
          </div>
        )}
      </div>

      <div className="panel flex flex-col gap-2 p-0">
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 pl-4 font-medium">{t("lineDescription")}</th>
                <th className="py-2 pr-4 font-medium">{t("lineQuantity")}</th>
                <th className="py-2 pr-4 font-medium">{t("lineUnitPrice")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnLineTotal")}</th>
              </tr>
            </thead>
            <tbody>
              {(order.lines ?? []).map((l) => (
                <tr key={l.id} className="border-b border-zinc-200 last:border-0 dark:border-zinc-800">
                  <td className="py-2 pr-4 pl-4">{l.description}</td>
                  <td className="py-2 pr-4 font-mono">{l.quantity}</td>
                  <td className="py-2 pr-4 font-mono">{l.unitPrice}</td>
                  <td className="py-2 pr-4 font-mono">{l.lineTotal}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {isOwner && manualOptions.length > 0 && (
        <div className="flex flex-col gap-2">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("changeStatus")}</h2>
          <div className="flex flex-wrap gap-2">
            {manualOptions.map((s) => (
              <button
                key={s}
                type="button"
                data-permission-public="true"
                disabled={changingStatus}
                onClick={() => changeStatus(s)}
                className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
              >
                {t(`status.${s}`)}
              </button>
            ))}
          </div>
          {statusError && <p className="text-sm text-red-600 dark:text-red-400">{statusError}</p>}
        </div>
      )}
    </div>
  );
}
