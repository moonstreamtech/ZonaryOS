"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import type { Product, StockLevel, StockMovement } from "@/lib/inventory";

type ProductWithStock = Product & { stock: StockLevel[] };

type Props = {
  firmId: string;
  products: ProductWithStock[];
  movements: StockMovement[];
  isOwner: boolean;
};

// isLowStock mirrors internal/reports' own low_stock_products KPI
// definition exactly (SUM per product/location < min_quantity, compared
// here per-product across every location the product has stock in) - the
// /inventory page's own visual highlight for the same condition the
// dashboard KPI counts, not a second independent definition of "low".
function isLowStock(p: ProductWithStock): boolean {
  const minQuantity = Number(p.minQuantity);
  if (!Number.isFinite(minQuantity) || minQuantity <= 0) return false;
  const totalQuantity = p.stock.reduce((sum, s) => sum + (Number(s.quantity) || 0), 0);
  return totalQuantity < minQuantity;
}

// The /inventory page's product catalog: product list with per-location
// stock levels, low-stock highlighting (min_quantity vs. on-hand), and
// recent movement history - create/edit are owner-gated (server-checked
// by internal/inventory's own CreateProduct/UpdateProduct; isOwner here
// only controls what renders), mirroring components/HR/PeopleManager.tsx's
// own create-form/row-action shape.
export default function InventoryManager({ firmId, products, movements, isOwner }: Props) {
  const t = useTranslations("Inventory");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [sku, setSku] = useState("");
  const [name, setName] = useState("");
  const [unit, setUnit] = useState("piece");
  const [unitPrice, setUnitPrice] = useState("");
  const [costPrice, setCostPrice] = useState("");
  const [minQuantity, setMinQuantity] = useState("");
  const [category, setCategory] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const [expandedProductId, setExpandedProductId] = useState<string | null>(null);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    const trimmedSku = sku.trim();
    const trimmedName = name.trim();
    if (!trimmedSku || !trimmedName) {
      setCreateError(t("createSkuNameRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/inventory/products/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          sku: trimmedSku,
          name: trimmedName,
          unit: unit || undefined,
          unitPrice: unitPrice || undefined,
          costPrice: costPrice || undefined,
          minQuantity: minQuantity || undefined,
          category: category || undefined,
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setSku("");
      setName("");
      setUnit("piece");
      setUnitPrice("");
      setCostPrice("");
      setMinQuantity("");
      setCategory("");
      router.refresh();
    } catch {
      setCreateError(t("createError"));
    } finally {
      setSubmitting(false);
    }
  }

  async function toggleActive(product: Product) {
    try {
      await fetch(`/api/inventory/products/${firmId}/${product.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ isActive: !product.isActive }),
      });
      router.refresh();
    } catch {
      // best-effort - the row simply won't reflect the change; a retry
      // is always available via the same button.
    }
  }

  return (
    <div className="flex w-full max-w-4xl flex-col gap-6">
      {isOwner && (
        <div className="flex items-center justify-between">
          <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
            {t("productsTitle")}
          </h2>
          <button
            type="button"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newProduct")}
          </button>
        </div>
      )}

      {creating && (
        <form
          onSubmit={submitCreate}
          className="flex flex-col gap-3 rounded-md border border-zinc-300 p-4 dark:border-zinc-700"
        >
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="flex flex-col gap-1 text-sm">
              {t("sku")}
              <input
                value={sku}
                onChange={(e) => setSku(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("name")}
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("unit")}
              <input
                value={unit}
                onChange={(e) => setUnit(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("category")}
              <input
                value={category}
                onChange={(e) => setCategory(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("unitPrice")}
              <input
                value={unitPrice}
                onChange={(e) => setUnitPrice(e.target.value)}
                placeholder="0.00"
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("costPrice")}
              <input
                value={costPrice}
                onChange={(e) => setCostPrice(e.target.value)}
                placeholder="0.00"
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("minQuantity")}
              <input
                value={minQuantity}
                onChange={(e) => setMinQuantity(e.target.value)}
                placeholder="0"
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
          </div>
          {createError && <p className="text-sm text-red-600 dark:text-red-400">{createError}</p>}
          <button
            type="submit"
            disabled={submitting}
            className="self-start rounded-md bg-black px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50 dark:bg-zinc-50 dark:text-black"
          >
            {t("create")}
          </button>
        </form>
      )}

      {products.length === 0 ? (
        <p className="text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
      ) : (
        <div className="flex flex-col gap-2">
          {products.map((product) => {
            const lowStock = isLowStock(product);
            return (
              <div
                key={product.id}
                className={`rounded-md border ${lowStock ? "border-amber-400 dark:border-amber-600" : "border-zinc-300 dark:border-zinc-700"}`}
              >
                <button
                  type="button"
                  onClick={() => setExpandedProductId((cur) => (cur === product.id ? null : product.id))}
                  className="flex w-full items-center justify-between gap-2 p-3 text-left text-sm"
                >
                  <span className="font-medium text-black dark:text-zinc-50">
                    {product.sku} · {product.name}
                  </span>
                  <span className="flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
                    {lowStock && (
                      <span className="rounded-full bg-amber-100 px-2 py-0.5 font-medium text-amber-800 dark:bg-amber-900 dark:text-amber-200">
                        {t("lowStock")}
                      </span>
                    )}
                    {product.isActive ? t("statusActive") : t("statusInactive")}
                  </span>
                </button>

                {expandedProductId === product.id && (
                  <div className="flex flex-col gap-3 border-t border-zinc-200 p-3 text-sm dark:border-zinc-800">
                    {isOwner && (
                      <button
                        type="button"
                        onClick={() => toggleActive(product)}
                        className="self-start rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                      >
                        {product.isActive ? t("deactivate") : t("activate")}
                      </button>
                    )}

                    <h3 className="text-xs font-medium text-zinc-600 dark:text-zinc-400">{t("stockTitle")}</h3>
                    {product.stock.length === 0 ? (
                      <p className="text-xs text-zinc-500 dark:text-zinc-500">{t("noStock")}</p>
                    ) : (
                      <ul className="flex flex-col gap-1">
                        {product.stock.map((s) => (
                          <li
                            key={s.location}
                            className="flex items-center justify-between font-mono text-xs text-zinc-700 dark:text-zinc-300"
                          >
                            <span>{s.location}</span>
                            <span>{s.quantity}</span>
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      <div className="flex flex-col gap-2">
        <h2 className="text-xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("movementsTitle")}
        </h2>
        {movements.length === 0 ? (
          <p className="text-zinc-600 dark:text-zinc-400">{t("noMovements")}</p>
        ) : (
          <div className="overflow-x-auto rounded-md border border-zinc-300 dark:border-zinc-700">
            <table className="w-full text-left text-xs">
              <thead className="border-b border-zinc-200 text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
                <tr>
                  <th className="p-2 font-medium">{t("locationColumn")}</th>
                  <th className="p-2 font-medium">{t("quantityColumn")}</th>
                  <th className="p-2 font-medium">{t("reasonColumn")}</th>
                </tr>
              </thead>
              <tbody>
                {movements.map((m) => (
                  <tr key={m.id} className="border-b border-zinc-100 dark:border-zinc-900">
                    <td className="p-2 font-mono text-zinc-700 dark:text-zinc-300">{m.location}</td>
                    <td className="p-2 font-mono text-zinc-700 dark:text-zinc-300">{m.quantityChange}</td>
                    <td className="p-2 text-zinc-500 dark:text-zinc-400">
                      {m.reason === "sale"
                        ? t("movementReasonSale")
                        : m.reason === "purchase"
                          ? t("movementReasonPurchase")
                          : m.reason}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
