// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchWarehouse, fetchWarehouseLocations, fetchWarehouseStock } from "@/lib/warehouse";
import WarehouseDetail from "@/components/Warehouse/WarehouseDetail";

type PageProps = {
  params: Promise<{ locale: string; warehouseId: string }>;
};

// Warehouse management batch's warehouse detail page: the aisle -> shelf
// -> bin location tree (built client-side from the flat locations list -
// see lib/warehouse.ts's fetchWarehouseLocations doc comment), a
// create-location form, and per-location stock. Member-gated read,
// owner-gated write.
export default async function WarehouseDetailPage({ params }: PageProps) {
  const { locale, warehouseId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Warehouse");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const warehouse = await fetchWarehouse(sessionToken, firm.firmId, warehouseId);
  const [locations, stock] = await Promise.all([
    warehouse ? fetchWarehouseLocations(sessionToken, firm.firmId, warehouseId) : Promise.resolve(null),
    warehouse ? fetchWarehouseStock(sessionToken, firm.firmId, warehouseId) : Promise.resolve(null),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {warehouse === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <WarehouseDetail
          firmId={firm.firmId}
          warehouse={warehouse}
          locations={locations ?? []}
          stock={stock ?? []}
          isOwner={isOwner}
        />
      )}
    </main>
  );
}
