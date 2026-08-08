// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchProducts, fetchStock, fetchStockMovements } from "@/lib/inventory";
import InventoryManager from "@/components/Inventory/InventoryManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Inventory management batch's product catalog page: product list with
// per-location stock levels and recent movement history. Member-gated
// read, owner-gated write (see internal/inventory's own package doc
// comment) - this page renders for any member, but InventoryManager only
// shows create/edit controls when isOwner (the backend enforces this
// regardless; the frontend gate is just so a non-owner isn't shown
// controls that would 403).
export default async function InventoryPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Inventory");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const products = await fetchProducts(sessionToken, firm.firmId);

  // Stock levels are fetched eagerly, per product, server-side - product
  // catalogs are small (dozens to low hundreds, not thousands) for any
  // firm this batch's scope targets, the same reasoning HRPage's own
  // per-person contract fetch already documents.
  const productsWithStock =
    products !== null
      ? await Promise.all(
          products.map(async (p) => ({
            ...p,
            stock: (await fetchStock(sessionToken, firm.firmId, p.id)) ?? [],
          })),
        )
      : null;

  const movementsResult = await fetchStockMovements(sessionToken, firm.firmId, { limit: 50 });

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("title")}
        </h1>
      </div>

      {productsWithStock === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <InventoryManager
          firmId={firm.firmId}
          products={productsWithStock}
          movements={movementsResult?.movements ?? []}
          isOwner={isOwner}
        />
      )}
    </main>
  );
}
