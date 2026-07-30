"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";

type Props = {
  firmId: string;
  definitionId: string;
  createPermissionKey: string;
};

// Open Points item 39: a form on the stock page to create a new
// workflow instance ("add stock"), calling the existing
// internal/workflow.CreateInstance through app/api/stock/create's proxy
// route - no new backend endpoint was needed (see docs/api/openapi.yaml's
// POST .../instances, already documented before this PR). item/quantity
// are the two payload fields StockList.tsx already knows how to render
// (instance.payload.item / .quantity) - this form is simply the other
// end of that same shape.
export default function AddStockForm({
  firmId,
  definitionId,
  createPermissionKey,
}: Props) {
  const t = useTranslations("Stock");
  const router = useRouter();
  const [item, setItem] = useState("");
  const [quantity, setQuantity] = useState("1");
  const [itemError, setItemError] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);

    if (item.trim() === "") {
      setItemError(true);
      return;
    }
    setItemError(false);

    const parsedQuantity = Number(quantity);
    const payload: Record<string, unknown> = { item: item.trim() };
    if (Number.isFinite(parsedQuantity) && parsedQuantity > 0) {
      payload.quantity = parsedQuantity;
    }

    setSubmitting(true);
    try {
      const res = await fetch("/api/stock/create", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ firmId, definitionId, payload }),
      });
      if (!res.ok) {
        setError(
          res.status === 403 ? t("addStockPermissionDenied") : t("addStockError"),
        );
        return;
      }
      setItem("");
      setQuantity("1");
      router.refresh();
    } catch {
      setError(t("addStockError"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form
      onSubmit={handleSubmit}
      className="flex w-full flex-col gap-3 rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-950"
    >
      <h2 className="text-sm font-medium text-zinc-700 dark:text-zinc-300">
        {t("addStockTitle")}
      </h2>

      {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}

      <div className="flex flex-wrap items-end gap-3">
        <div className="flex flex-1 min-w-[10rem] flex-col gap-1">
          <label
            htmlFor="stock-item"
            className="text-xs text-zinc-600 dark:text-zinc-400"
          >
            {t("addStockItemLabel")}
          </label>
          <input
            id="stock-item"
            type="text"
            value={item}
            onChange={(e) => {
              setItem(e.target.value);
              setItemError(false);
            }}
            placeholder={t("addStockItemPlaceholder")}
            className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
          />
          {itemError && (
            <p className="text-xs text-red-600 dark:text-red-400">
              {t("addStockItemRequired")}
            </p>
          )}
        </div>

        <div className="flex w-24 flex-col gap-1">
          <label
            htmlFor="stock-quantity"
            className="text-xs text-zinc-600 dark:text-zinc-400"
          >
            {t("addStockQuantityLabel")}
          </label>
          <input
            id="stock-quantity"
            type="number"
            min={1}
            value={quantity}
            onChange={(e) => setQuantity(e.target.value)}
            className="rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm text-black dark:border-zinc-700 dark:bg-zinc-900 dark:text-white"
          />
        </div>

        <button
          type="submit"
          data-permission-key={createPermissionKey}
          disabled={submitting}
          className="rounded-full bg-foreground px-4 py-1.5 text-xs font-medium text-background transition-colors hover:bg-[#383838] disabled:opacity-50 dark:hover:bg-[#ccc]"
        >
          {t("addStockSubmit")}
        </button>
      </div>
    </form>
  );
}
