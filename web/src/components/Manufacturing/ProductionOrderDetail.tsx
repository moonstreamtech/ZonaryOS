"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { ProductionOrder } from "@/lib/manufacturing";
import type { Product } from "@/lib/inventory";

type Props = {
  firmId: string;
  order: ProductionOrder;
  products: Product[];
  isOwner: boolean;
};

function productName(products: Product[], id: string): string {
  return products.find((p) => p.id === id)?.name ?? id;
}

// Start/Complete/Cancel drive internal/manufacturing's own atomic
// transitions (material issue on start, finished-good stock + balanced
// journal entry on complete) - this component only offers the buttons
// the order's current status allows; the server re-validates regardless
// (ErrInvalidProductionOrderTransition), same "server is the actual
// authority" convention every other status-change control in this app
// follows (e.g. components/Invoicing/InvoiceDetail.tsx).
export default function ProductionOrderDetail({ firmId, order, products, isOwner }: Props) {
  const t = useTranslations("Manufacturing");
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [quantityProduced, setQuantityProduced] = useState(order.quantityPlanned);

  async function callAction(path: string, body?: unknown) {
    setActionError(null);
    setBusy(true);
    try {
      const res = await fetch(`/api/manufacturing/production-orders/${firmId}/${order.id}/${path}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (!res.ok) {
        setActionError(t("actionError"));
        return;
      }
      router.refresh();
    } catch {
      setActionError(t("actionError"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex w-full max-w-2xl flex-col gap-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight text-black dark:text-zinc-50">{order.orderNumber}</h1>
        <span className="text-sm text-zinc-600 dark:text-zinc-400">{t(`status.${order.status}`)}</span>
      </div>

      <div className="panel flex flex-col gap-2 p-4 text-sm">
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("columnProduct")}</span>
          <span>{productName(products, order.productId)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("quantityPlanned")}</span>
          <span className="font-mono">{order.quantityPlanned}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("quantityProduced")}</span>
          <span className="font-mono">{order.quantityProduced}</span>
        </div>
      </div>

      <div className="panel flex flex-col gap-2 p-0">
        <h2 className="px-4 pt-4 text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("materialIssuesTitle")}</h2>
        {(order.materialIssues ?? []).length === 0 ? (
          <p className="px-4 pb-4 text-sm text-zinc-600 dark:text-zinc-400">{t("noMaterialIssues")}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 pl-4 font-medium">{t("componentProduct")}</th>
                  <th className="py-2 pr-4 font-medium">{t("quantityIssued")}</th>
                  <th className="py-2 pr-4 font-medium">{t("issuedAt")}</th>
                </tr>
              </thead>
              <tbody>
                {(order.materialIssues ?? []).map((m) => (
                  <tr key={m.id} className="border-b border-zinc-200 last:border-0 dark:border-zinc-800">
                    <td className="py-2 pr-4 pl-4">{productName(products, m.productId)}</td>
                    <td className="py-2 pr-4 font-mono">{m.quantityIssued}</td>
                    <td className="py-2 pr-4">{new Date(m.issuedAt).toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {isOwner && (order.status === "planned" || order.status === "in_progress") && (
        <div className="flex flex-col gap-3">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("actionsTitle")}</h2>
          <div className="flex flex-wrap items-center gap-2">
            {order.status === "planned" && (
              <button
                type="button"
                data-permission-public="true"
                disabled={busy}
                onClick={() => callAction("start")}
                className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
              >
                {t("startAction")}
              </button>
            )}
            {order.status === "in_progress" && (
              <>
                <input
                  value={quantityProduced}
                  onChange={(e) => setQuantityProduced(e.target.value)}
                  data-permission-public="true"
                  className="w-28 rounded-md border border-zinc-300 px-2 py-1.5 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                />
                <button
                  type="button"
                  data-permission-public="true"
                  disabled={busy}
                  onClick={() => callAction("complete", { quantityProduced })}
                  className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                >
                  {t("completeAction")}
                </button>
              </>
            )}
            <button
              type="button"
              data-permission-public="true"
              disabled={busy}
              onClick={() => callAction("cancel")}
              className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
            >
              {t("cancelAction")}
            </button>
          </div>
          {actionError && <p className="text-sm text-red-600 dark:text-red-400">{actionError}</p>}
        </div>
      )}
    </div>
  );
}
