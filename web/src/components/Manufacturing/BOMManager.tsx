"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import type { BOM } from "@/lib/manufacturing";
import type { Product } from "@/lib/inventory";
import EmptyState from "@/components/ui/EmptyState";

type Props = {
  firmId: string;
  boms: BOM[];
  products: Product[];
  isOwner: boolean;
};

function productName(products: Product[], id: string): string {
  return products.find((p) => p.id === id)?.name ?? id;
}

// The /manufacturing/bom page's BOM list - mirrors
// components/SalesOrders/SalesOrdersManager.tsx's own create-form/row
// shape: create is owner-gated (server-checked by
// internal/manufacturing's own CreateBOM; isOwner here only controls what
// renders), the create form is deliberately minimal - one component line
// per BOM at creation time, matching this codebase's "functional, not
// polished" first-pass convention (adding further components/version
// management happens on the detail page).
export default function BOMManager({ firmId, boms, products, isOwner }: Props) {
  const t = useTranslations("Manufacturing");
  const router = useRouter();

  const [creating, setCreating] = useState(false);
  const [productId, setProductId] = useState("");
  const [name, setName] = useState("");
  const [componentProductId, setComponentProductId] = useState("");
  const [quantityPerUnit, setQuantityPerUnit] = useState("1");
  const [createError, setCreateError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    setCreateError(null);
    if (!productId || !name.trim() || !componentProductId || !quantityPerUnit) {
      setCreateError(t("createRequired"));
      return;
    }

    setSubmitting(true);
    try {
      const res = await fetch(`/api/manufacturing/boms/${firmId}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          productId,
          name: name.trim(),
          lines: [{ componentProductId, quantityPerUnit }],
        }),
      });
      if (!res.ok) {
        setCreateError(t("createError"));
        return;
      }
      setCreating(false);
      setProductId("");
      setName("");
      setComponentProductId("");
      setQuantityPerUnit("1");
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
            {t("bomListTitle")}
          </h2>
          <button
            type="button"
            data-permission-public="true"
            onClick={() => setCreating((v) => !v)}
            className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm font-medium text-black hover:bg-zinc-100 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
          >
            {creating ? t("cancel") : t("newBOM")}
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
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("bomName")}
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              />
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("componentProduct")}
              <select
                value={componentProductId}
                onChange={(e) => setComponentProductId(e.target.value)}
                className="rounded-md border border-zinc-300 px-2 py-1.5 dark:border-zinc-700 dark:bg-black dark:text-zinc-50"
              >
                <option value="">{t("selectProduct")}</option>
                {products.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-sm">
              {t("quantityPerUnit")}
              <input
                value={quantityPerUnit}
                onChange={(e) => setQuantityPerUnit(e.target.value)}
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

      {boms.length === 0 ? (
        <EmptyState message={t("bomEmpty")} />
      ) : (
        <div className="panel flex flex-col gap-3 p-0">
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm">
              <thead>
                <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                  <th className="py-2 pr-4 pl-4 font-medium">{t("columnName")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnProduct")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnVersion")}</th>
                  <th className="py-2 pr-4 font-medium">{t("columnActive")}</th>
                </tr>
              </thead>
              <tbody>
                {boms.map((b) => (
                  <tr
                    key={b.id}
                    className="border-b border-zinc-200 text-black last:border-0 hover:bg-zinc-50 dark:border-zinc-800 dark:text-zinc-50 dark:hover:bg-zinc-950"
                  >
                    <td className="py-2 pr-4 pl-4">
                      <Link
                        href={`/manufacturing/bom/${b.id}`}
                        data-permission-public="true"
                        className="underline-offset-4 hover:underline"
                      >
                        {b.name}
                      </Link>
                    </td>
                    <td className="py-2 pr-4">{productName(products, b.productId)}</td>
                    <td className="py-2 pr-4 font-mono">{b.version}</td>
                    <td className="py-2 pr-4">{b.isActive ? t("active") : t("inactive")}</td>
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
