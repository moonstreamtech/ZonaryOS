"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import type { CycleCountSession, Location, Warehouse } from "@/lib/warehouse";
import type { Product } from "@/lib/inventory";
import EmptyState from "@/components/ui/EmptyState";

type Props = {
  firmId: string;
  sessions: CycleCountSession[];
  warehouses: Warehouse[];
  products: Product[];
  locationsByWarehouse: { warehouse: Warehouse; locations: Location[] }[];
  isOwner: boolean;
};

type DraftLine = { locationId: string; productId: string };

function warehouseName(warehouses: Warehouse[], id: string): string {
  return warehouses.find((w) => w.id === id)?.name ?? id;
}

// The /inventory/cycle-counts page's session list - mirrors
// components/Manufacturing/BOMManager.tsx's own create-form/row shape,
// with a two-step create form (item 2c of the batch brief): pick a
// warehouse, then pick one or more (location, product) pairs to snapshot
// as lines - internal/warehouse.CreateCycleCount snapshots each line's
// `systemQuantity` from current stock server-side, so only the pair
// identifiers are sent.
export default function CycleCountsManager({
  firmId,
  sessions,
  warehouses,
  products,
  locationsByWarehouse,
  isOwner,
}: Props) {
  const t = useTranslations("Warehouse");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [warehouseId, setWarehouseId] = useState("");
  const [lineLocationId, setLineLocationId] = useState("");
  const [lineProductId, setLineProductId] = useState("");
  const [lines, setLines] = useState<DraftLine[]>([]);
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const locationsForWarehouse = locationsByWarehouse.find((lw) => lw.warehouse.id === warehouseId)?.locations ?? [];

  function addLine() {
    if (!lineLocationId || !lineProductId) return;
    if (lines.some((l) => l.locationId === lineLocationId && l.productId === lineProductId)) return;
    setLines((prev) => [...prev, { locationId: lineLocationId, productId: lineProductId }]);
    setLineLocationId("");
    setLineProductId("");
  }

  function removeLine(idx: number) {
    setLines((prev) => prev.filter((_, i) => i !== idx));
  }

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!warehouseId || lines.length === 0) {
      setCreateError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/warehouse/cycle-counts/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ warehouseId, lines }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setWarehouseId("");
      setLines([]);
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      {isOwner && (
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
            {t("cycleCountsListTitle")}
          </h2>
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newCycleCount")}
          </button>
        </div>
      )}

      {creating && (
        <form
          onSubmit={submitCreate}
          data-permission-public="true"
          className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
        >
          <label className="flex flex-col gap-1 text-sm sm:w-1/2">
            {t("columnWarehouse")}
            <select
              value={warehouseId}
              onChange={(e) => {
                setWarehouseId(e.target.value);
                setLineLocationId("");
                setLines([]);
              }}
              className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
            >
              <option value="">{t("selectWarehouse")}</option>
              {warehouses.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.name}
                </option>
              ))}
            </select>
          </label>

          {warehouseId && (
            <div className="flex flex-col gap-2 rounded-md border border-zinc-200 p-3 dark:border-zinc-800">
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                <label className="flex flex-col gap-1 text-sm">
                  {t("columnLocation")}
                  <select
                    value={lineLocationId}
                    onChange={(e) => setLineLocationId(e.target.value)}
                    className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                  >
                    <option value="">{t("selectLocation")}</option>
                    {locationsForWarehouse.map((l) => (
                      <option key={l.id} value={l.id}>
                        {l.code} {l.name ? `– ${l.name}` : ""}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="flex flex-col gap-1 text-sm">
                  {t("columnProduct")}
                  <select
                    value={lineProductId}
                    onChange={(e) => setLineProductId(e.target.value)}
                    className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
                  >
                    <option value="">{t("selectProduct")}</option>
                    {products.map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.sku} – {p.name}
                      </option>
                    ))}
                  </select>
                </label>
                <button
                  type="button"
                  data-permission-public="true"
                  onClick={addLine}
                  disabled={!lineLocationId || !lineProductId}
                  className="mt-auto self-start rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                >
                  {t("addLine")}
                </button>
              </div>

              {lines.length > 0 && (
                <ul className="flex flex-col gap-1 text-sm">
                  {lines.map((l, idx) => {
                    const loc = locationsForWarehouse.find((x) => x.id === l.locationId);
                    const prod = products.find((x) => x.id === l.productId);
                    return (
                      <li key={`${l.locationId}-${l.productId}`} className="flex items-center justify-between gap-2">
                        <span>
                          {loc?.code ?? l.locationId} · {prod ? `${prod.sku} – ${prod.name}` : l.productId}
                        </span>
                        <button
                          type="button"
                          data-permission-public="true"
                          onClick={() => removeLine(idx)}
                          className="text-xs text-zinc-500 hover:underline dark:text-zinc-400"
                        >
                          {t("removeLine")}
                        </button>
                      </li>
                    );
                  })}
                </ul>
              )}
            </div>
          )}

          {createError && <p className="text-sm text-red-600 dark:text-red-400">{createError}</p>}
          <button
            type="submit"
            data-permission-public="true"
            disabled={submitting}
            className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
          >
            {t("create")}
          </button>
        </form>
      )}

      {sessions.length === 0 ? (
        <EmptyState message={t("cycleCountsEmpty")} />
      ) : (
        <div className="panel flex flex-col gap-3 p-0">
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 pl-4 font-medium">{t("columnWarehouse")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnStatus")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnCreatedAt")}</th>
                </tr>
              </thead>
              <tbody>
                {sessions.map((s) => (
                  <tr
                    key={s.id}
                    className="border-b border-zinc-200 text-black last:border-0 hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-50 dark:hover:bg-zinc-950"
                  >
                    <td className="py-2 pr-4 pl-4">
                      <Link
                        href={`/inventory/cycle-counts/${s.id}`}
                        data-permission-public="true"
                        className="underline-offset-4 hover:underline"
                      >
                        {warehouseName(warehouses, s.warehouseId)}
                      </Link>
                    </td>
                    <td className="py-2 pr-4">{t(`sessionStatus.${s.status}`)}</td>
                    <td className="py-2 pr-4">{new Date(s.createdAt).toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
