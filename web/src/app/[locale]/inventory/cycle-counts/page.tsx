// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchCycleCounts, fetchWarehouses, fetchWarehouseLocations, type Location } from "@/lib/warehouse";
import { fetchProducts } from "@/lib/inventory";
import CycleCountsManager from "@/components/Warehouse/CycleCountsManager";
import ListPageHeader from "@/components/ui/ListPageHeader";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Warehouse management batch's cycle-count session list: a snapshot of
// system quantities for a chosen set of (location, product) pairs in one
// warehouse (internal/warehouse.CycleCountSession), counted and optionally
// reconciled against real stock on completion. Member-gated read,
// owner-gated write. Session creation lives on this list page - the
// warehouse/location/product pickers it needs are the same eager
// server-side fetch TransfersPage's own doc comment explains.
export default async function CycleCountsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Warehouse");
  const tNav = await getTranslations("Nav");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [sessions, warehouses, products] = await Promise.all([
    fetchCycleCounts(sessionToken, firm.firmId),
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
          title={t("cycleCountsTitle")}
          breadcrumb={[{ label: tNav("brand"), href: "/" }, { label: t("cycleCountsTitle") }]}
        />
      </div>

      {sessions === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <CycleCountsManager
          firmId={firm.firmId}
          sessions={sessions}
          warehouses={warehouses ?? []}
          products={products ?? []}
          locationsByWarehouse={locationsByWarehouse}
          isOwner={isOwner}
        />
      )}
    </main>
  );
}
