"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useState } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import type { BOM } from "@/lib/manufacturing";
import type { Product } from "@/lib/inventory";

type Props = {
  firmId: string;
  bom: BOM;
  otherVersions: BOM[];
  products: Product[];
  isOwner: boolean;
};

function productName(products: Product[], id: string): string {
  return products.find((p) => p.id === id)?.name ?? id;
}

export default function BOMDetail({ firmId, bom, otherVersions, products, isOwner }: Props) {
  const t = useTranslations("Manufacturing");
  const router = useRouter();
  const [activating, setActivating] = useState(false);
  const [activateError, setActivateError] = useState<string | null>(null);

  async function setActive(targetBomId: string) {
    setActivateError(null);
    setActivating(true);
    try {
      const res = await fetch(`/api/manufacturing/boms/${firmId}/${targetBomId}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ isActive: true }),
      });
      if (!res.ok) {
        setActivateError(t("activateError"));
        return;
      }
      router.refresh();
    } catch {
      setActivateError(t("activateError"));
    } finally {
      setActivating(false);
    }
  }

  return (
    <div className="flex w-full max-w-2xl flex-col gap-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight text-black dark:text-zinc-50">{bom.name}</h1>
        <span className="text-sm text-zinc-600 dark:text-zinc-400">
          {t("columnVersion")}: {bom.version} · {bom.isActive ? t("active") : t("inactive")}
        </span>
      </div>

      <div className="panel flex flex-col gap-2 p-4 text-sm">
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("columnProduct")}</span>
          <span>{productName(products, bom.productId)}</span>
        </div>
        <div className="flex justify-between">
          <span className="text-zinc-600 dark:text-zinc-400">{t("unitYield")}</span>
          <span className="font-mono">{bom.unitYield}</span>
        </div>
      </div>

      <div className="panel flex flex-col gap-2 p-0">
        <h2 className="px-4 pt-4 text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("componentsTitle")}</h2>
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-sm">
            <thead>
              <tr className="border-b border-zinc-300 text-zinc-600 dark:border-zinc-700 dark:text-zinc-400">
                <th className="py-2 pr-4 pl-4 font-medium">{t("componentProduct")}</th>
                <th className="py-2 pr-4 font-medium">{t("quantityPerUnit")}</th>
                <th className="py-2 pr-4 font-medium">{t("unit")}</th>
              </tr>
            </thead>
            <tbody>
              {(bom.lines ?? []).map((l) => (
                <tr key={l.id} className="border-b border-zinc-200 last:border-0 dark:border-zinc-800">
                  <td className="py-2 pr-4 pl-4">{productName(products, l.componentProductId)}</td>
                  <td className="py-2 pr-4 font-mono">{l.quantityPerUnit}</td>
                  <td className="py-2 pr-4">{l.unit}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {otherVersions.length > 0 && (
        <div className="flex flex-col gap-2">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">{t("otherVersionsTitle")}</h2>
          <div className="panel flex flex-col gap-2 p-0">
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left text-sm">
                <tbody>
                  {otherVersions.map((v) => (
                    <tr key={v.id} className="border-b border-zinc-200 last:border-0 dark:border-zinc-800">
                      <td className="py-2 pr-4 pl-4">
                        <Link href={`/manufacturing/bom/${v.id}`} data-permission-public="true" className="underline-offset-4 hover:underline">
                          {t("columnVersion")} {v.version}
                        </Link>
                      </td>
                      <td className="py-2 pr-4">{v.isActive ? t("active") : t("inactive")}</td>
                      <td className="py-2 pr-4 pr-4">
                        {isOwner && !v.isActive && (
                          <button
                            type="button"
                            data-permission-public="true"
                            disabled={activating}
                            onClick={() => setActive(v.id)}
                            className="rounded-md border border-zinc-300 px-2 py-1 text-xs font-medium text-black hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-50 dark:hover:bg-zinc-900"
                          >
                            {t("setActive")}
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
          {activateError && <p className="text-sm text-red-600 dark:text-red-400">{activateError}</p>}
        </div>
      )}
    </div>
  );
}
