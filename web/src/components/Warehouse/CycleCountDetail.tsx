"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { CycleCountSession, Location } from "@/lib/warehouse";
import type { Product } from "@/lib/inventory";

type Props = {
  firmId: string;
  session: CycleCountSession;
  warehouseName: string;
  locations: Location[];
  products: Product[];
  isOwner: boolean;
};

function locationLabel(locations: Location[], id: string): string {
  const l = locations.find((l) => l.id === id);
  return l ? l.code : id;
}

function productLabel(products: Product[], id: string): string {
  const p = products.find((p) => p.id === id);
  return p ? `${p.sku} – ${p.name}` : id;
}

// The /inventory/cycle-counts/[sessionId] detail page - mirrors
// components/Manufacturing/ProductionOrderDetail.tsx's own
// status-gated-actions layout. Recording a count is per-line
// (internal/warehouse.RecordCycleCountLine, owner-only); completing the
// whole session (internal/warehouse.CompleteCycleCount, also owner-only)
// takes an "adjust stock" toggle - when checked, every line with a
// recorded count and non-zero variance gets a real stock adjustment plus
// a balanced journal entry, server-side.
export default function CycleCountDetail({ firmId, session, warehouseName, locations, products, isOwner }: Props) {
  const t = useTranslations("Warehouse");
  const router = useRouter();

  const [counts, setCounts] = useState<Record<string, string>>({});
  const [busyLineId, setBusyLineId] = useState<string | null>(null);
  const [lineErrors, setLineErrors] = useState<Record<string, string>>({});

  const [adjustStock, setAdjustStock] = useState(false);
  const [completing, setCompleting] = useState(false);
  const [completeError, setCompleteError] = useState<string | null>(null);

  async function recordCount(lineId: string) {
    const value = counts[lineId];
    if (!value) return;
    setLineErrors((prev) => ({ ...prev, [lineId]: "" }));
    setBusyLineId(lineId);
    try {
      const res = await fetch(`/api/warehouse/cycle-counts/${firmId}/${session.id}/lines/${lineId}/count`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ countedQuantity: value }),
      });
      if (!res.ok) {
        setLineErrors((prev) => ({ ...prev, [lineId]: t("countError") }));
        return;
      }
      router.refresh();
    } catch {
      setLineErrors((prev) => ({ ...prev, [lineId]: t("countError") }));
    } finally {
      setBusyLineId(null);
    }
  }

  async function completeSession() {
    setCompleteError(null);
    setCompleting(true);
    try {
      const res = await fetch(`/api/warehouse/cycle-counts/${firmId}/${session.id}/complete`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ adjust: adjustStock }),
      });
      if (!res.ok) {
        setCompleteError(t("sessionCompleteError"));
        return;
      }
      router.refresh();
    } catch {
      setCompleteError(t("sessionCompleteError"));
    } finally {
      setCompleting(false);
    }
  }

  return (
    <div className="flex w-full max-w-3xl flex-col gap-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight text-black dark:text-zinc-50">{warehouseName}</h1>
        <span className="text-sm text-zinc-600 dark:text-zinc-400">{t(`sessionStatus.${session.status}`)}</span>
      </div>

      <div className="panel flex flex-col gap-2 p-0">
        <h2 className="px-4 pt-4 text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("linesTitle")}</h2>
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 pl-4 font-medium">{t("columnLocation")}</th>
                <th className="py-2 pr-4 font-medium">{t("columnProduct")}</th>
                <th className="py-2 pr-4 font-medium">{t("systemQuantity")}</th>
                <th className="py-2 pr-4 font-medium">{t("countedQuantity")}</th>
                <th className="py-2 pr-4 font-medium">{t("variance")}</th>
                {isOwner && session.status === "open" && (
                  <th className="py-2 pr-4 font-medium">{t("actionsTitle")}</th>
                )}
              </tr>
            </thead>
            <tbody>
              {session.lines.map((line) => (
                <tr key={line.id} className="border-b border-zinc-200 last:border-0 dark:border-zinc-800">
                  <td className="py-2 pr-4 pl-4 font-mono text-xs">{locationLabel(locations, line.locationId)}</td>
                  <td className="py-2 pr-4">{productLabel(products, line.productId)}</td>
                  <td className="py-2 pr-4 font-mono">{line.systemQuantity}</td>
                  <td className="py-2 pr-4 font-mono">
                    {line.countedQuantity !== undefined ? (
                      line.countedQuantity
                    ) : isOwner && session.status === "open" ? (
                      <input
                        value={counts[line.id] ?? ""}
                        onChange={(e) => setCounts((prev) => ({ ...prev, [line.id]: e.target.value }))}
                        data-permission-public="true"
                        className="w-24 rounded-md border border-zinc-300 px-2 py-1 text-sm dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                      />
                    ) : (
                      t("notCounted")
                    )}
                  </td>
                  <td className="py-2 pr-4 font-mono">{line.countedQuantity !== undefined ? line.variance : ""}</td>
                  {isOwner && session.status === "open" && (
                    <td className="py-2 pr-4">
                      {line.countedQuantity === undefined && (
                        <div className="flex flex-col gap-1">
                          <button
                            type="button"
                            data-permission-public="true"
                            disabled={busyLineId === line.id || !counts[line.id]}
                            onClick={() => recordCount(line.id)}
                            className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                          >
                            {t("recordCount")}
                          </button>
                          {lineErrors[line.id] && (
                            <p className="text-xs text-red-600 dark:text-red-400">{lineErrors[line.id]}</p>
                          )}
                        </div>
                      )}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {isOwner && session.status === "open" && (
        <div className="flex flex-col gap-3">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("actionsTitle")}</h2>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={adjustStock}
              onChange={(e) => setAdjustStock(e.target.checked)}
              data-permission-public="true"
              className="h-4 w-4"
            />
            {t("adjustStock")}
          </label>
          <button
            type="button"
            data-permission-public="true"
            disabled={completing}
            onClick={completeSession}
            className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
          >
            {t("completeSession")}
          </button>
          {completeError && <p className="text-sm text-red-600 dark:text-red-400">{completeError}</p>}
        </div>
      )}
    </div>
  );
}
