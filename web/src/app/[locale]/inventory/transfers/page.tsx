// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchInventoryTransfers, fetchWarehouses, fetchWarehouseLocations, type Location } from "@/lib/warehouse";
import { fetchProducts } from "@/lib/inventory";
import TransfersManager from "@/components/Warehouse/TransfersManager";
import ListPageHeader from "@/components/ui/ListPageHeader";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Warehouse management batch's inventory transfer list: location-to-location
// stock moves (internal/warehouse.InventoryTransfer), draft until completed.
// Member-gated read, owner-gated write. The from/to location pickers need
// every location across every warehouse in the firm - firm warehouse
// counts are small (same "dozens, not thousands" assumption InventoryPage's
// own per-product stock fetch already documents), so all of them are
// fetched eagerly, server-side, rather than lazily per selected warehouse.
export default async function TransfersPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Warehouse");
  const tNav = await getTranslations("Nav");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [transfers, warehouses, products] = await Promise.all([
    fetchInventoryTransfers(sessionToken, firm.firmId),
    fetchWarehouses(sessionToken, firm.firmId),
    fetchProducts(sessionToken, firm.firmId, { activeOnly: true }),
  ]);

  const locationsByWarehouse = await Promise.all(
    (warehouses ?? []).map(async (w) => ({
      warehouse: w,
      locations: (await fetchWarehouseLocations(sessionToken, firm.firmId, w.id)) ?? ([] as Location[]),
    })),
  );

  return (
    <main className="flex flex-1 flex-col items-center gap-10 px-6 py-10">
      <div className="w-full max-w-4xl">
        <ListPageHeader
          title={t("transfersTitle")}
          breadcrumb={[{ label: tNav("brand"), href: "/" }, { label: t("transfersTitle") }]}
        />
      </div>

      {transfers === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <TransfersManager
          firmId={firm.firmId}
          transfers={transfers}
          products={products ?? []}
          locationsByWarehouse={locationsByWarehouse}
          isOwner={isOwner}
        />
      )}
    </main>
  );
}
