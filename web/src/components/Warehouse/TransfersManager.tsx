"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { InventoryTransfer, Location, Warehouse } from "@/lib/warehouse";
import type { Product } from "@/lib/inventory";
import EmptyState from "@/components/ui/EmptyState";

type Props = {
  firmId: string;
  transfers: InventoryTransfer[];
  products: Product[];
  locationsByWarehouse: { warehouse: Warehouse; locations: Location[] }[];
  isOwner: boolean;
};

function productLabel(products: Product[], id: string): string {
  const p = products.find((p) => p.id === id);
  return p ? `${p.sku} – ${p.name}` : id;
}

function locationLabel(locationsByWarehouse: Props["locationsByWarehouse"], locationId: string): string {
  for (const { warehouse, locations } of locationsByWarehouse) {
    const loc = locations.find((l) => l.id === locationId);
    if (loc) return `${warehouse.name} / ${loc.code}`;
  }
  return locationId;
}

// The /inventory/transfers page's transfer list - mirrors
// components/Manufacturing/ProductionOrdersManager.tsx's own
// create-form/row/action shape. Complete/cancel per row are owner-gated
// (server-checked by internal/warehouse's own transfer lifecycle
// endpoints); a failed complete (e.g. insufficient stock at the source
// location) surfaces the backend's own plain-text error message inline,
// per row, rather than a generic one.
export default function TransfersManager({ firmId, transfers, products, locationsByWarehouse, isOwner }: Props) {
  const t = useTranslations("Warehouse");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [productId, setProductId] = useState("");
  const [fromLocationId, setFromLocationId] = useState("");
  const [toLocationId, setToLocationId] = useState("");
  const [quantity, setQuantity] = useState("1");
  const [notes, setNotes] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [busyId, setBusyId] = useState<string | null>(null);
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({});

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!productId || !fromLocationId || !toLocationId || !quantity) {
      setCreateError(t("createRequired"));
      return;
    }
    if (fromLocationId === toLocationId) {
      setCreateError(t("sameLocationError"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/warehouse/transfers/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          productId,
          fromLocationId,
          toLocationId,
          quantity,
          notes: notes.trim() || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setProductId("");
      setFromLocationId("");
      setToLocationId("");
      setQuantity("1");
      setNotes("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function callAction(transferId: string, action: "complete" | "cancel") {
    setRowErrors((prev) => ({ ...prev, [transferId]: "" }));
    setBusyId(transferId);
    try {
      const res = await fetch(`/api/warehouse/transfers/${firmId}/${transferId}/${action}`, {
        method: "POST",
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { error?: string };
        setRowErrors((prev) => ({ ...prev, [transferId]: body.error || t("actionError") }));
        return;
      }
      router.refresh();
    } catch {
      setRowErrors((prev) => ({ ...prev, [transferId]: t("actionError") }));
    } finally {
      setBusyId(null);
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      {isOwner && (
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
            {t("transfersListTitle")}
          </h2>
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newTransfer")}
          </button>
        </div>
      )}

      {creating && (
        <form
          onSubmit={submitCreate}
          data-permission-public="true"
          className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
        >
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-sm">
              {t("product")}
              <select
                value={productId}
                onChange={(e) => setProductId(e.target.value)}
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
            <label className="flex flex-col gap-1 text-sm">
              {t("quantity")}
              <input
                value={quantity}
                onChange={(e) => setQuantity(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("fromLocation")}
              <select
                value={fromLocationId}
                onChange={(e) => setFromLocationId(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                <option value="">{t("selectLocation")}</option>
                {locationsByWarehouse.map(({ warehouse, locations }) => (
                  <optgroup key={warehouse.id} label={warehouse.name}>
                    {locations.map((l) => (
                      <option key={l.id} value={l.id}>
                        {l.code} {l.name ? `– ${l.name}` : ""}
                      </option>
                    ))}
                  </optgroup>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("toLocation")}
              <select
                value={toLocationId}
                onChange={(e) => setToLocationId(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                <option value="">{t("selectLocation")}</option>
                {locationsByWarehouse.map(({ warehouse, locations }) => (
                  <optgroup key={warehouse.id} label={warehouse.name}>
                    {locations.map((l) => (
                      <option key={l.id} value={l.id}>
                        {l.code} {l.name ? `– ${l.name}` : ""}
                      </option>
                    ))}
                  </optgroup>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm sm:col-span-2">
              {t("notes")}
              <input
                value={notes}
                onChange={(e) => setNotes(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
          </div>
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

      {transfers.length === 0 ? (
        <EmptyState message={t("transfersEmpty")} />
      ) : (
        <div className="panel flex flex-col gap-3 p-0">
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 pl-4 font-medium">{t("columnProduct")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnFrom")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnTo")}</th>
                  <th className="py-2 pr-4 font-medium">{t("quantity")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnStatus")}</th>
                  {isOwner && <th className="py-2 pr-4 font-medium">{t("actionsTitle")}</th>}
                </tr>
              </thead>
              <tbody>
                {transfers.map((tr) => (
                  <tr key={tr.id} className="border-b border-zinc-200 text-black last:border-0 dark:border-zinc-800 dark:text-zinc-50">
                    <td className="py-2 pr-4 pl-4">{productLabel(products, tr.productId)}</td>
                    <td className="py-2 pr-4 font-mono text-xs">{locationLabel(locationsByWarehouse, tr.fromLocationId)}</td>
                    <td className="py-2 pr-4 font-mono text-xs">{locationLabel(locationsByWarehouse, tr.toLocationId)}</td>
                    <td className="py-2 pr-4 font-mono">{tr.quantity}</td>
                    <td className="py-2 pr-4">{t(`transferStatus.${tr.status}`)}</td>
                    {isOwner && (
                      <td className="py-2 pr-4">
                        {tr.status === "draft" && (
                          <div className="flex flex-col gap-1">
                            <div className="flex items-center gap-2">
                              <button
                                type="button"
                                data-permission-public="true"
                                disabled={busyId === tr.id}
                                onClick={() => callAction(tr.id, "complete")}
                                className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                              >
                                {t("completeAction")}
                              </button>
                              <button
                                type="button"
                                data-permission-public="true"
                                disabled={busyId === tr.id}
                                onClick={() => callAction(tr.id, "cancel")}
                                className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                              >
                                {t("cancelAction")}
                              </button>
                            </div>
                            {rowErrors[tr.id] && (
                              <p className="text-xs text-red-600 dark:text-red-400">{rowErrors[tr.id]}</p>
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
      )}
    </div>
  );
}
