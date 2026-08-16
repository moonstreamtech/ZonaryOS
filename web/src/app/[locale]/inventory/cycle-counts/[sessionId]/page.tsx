// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchCycleCount, fetchWarehouse, fetchWarehouseLocations } from "@/lib/warehouse";
import { fetchProducts } from "@/lib/inventory";
import CycleCountDetail from "@/components/Warehouse/CycleCountDetail";

type PageProps = {
  params: Promise<{ locale: string; sessionId: string }>;
};

// Warehouse management batch's cycle-count session detail page: the
// snapshot lines table (system quantity vs. recorded count vs. variance)
// and the record-count / complete-session actions. Member-gated read,
// owner-gated write.
export default async function CycleCountDetailPage({ params }: PageProps) {
  const { locale, sessionId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Warehouse");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const session = await fetchCycleCount(sessionToken, firm.firmId, sessionId);
  const [warehouse, locations, products] = await Promise.all([
    session ? fetchWarehouse(sessionToken, firm.firmId, session.warehouseId) : Promise.resolve(null),
    session ? fetchWarehouseLocations(sessionToken, firm.firmId, session.warehouseId) : Promise.resolve(null),
    fetchProducts(sessionToken, firm.firmId, { activeOnly: true }),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {session === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <CycleCountDetail
          firmId={firm.firmId}
          session={session}
          warehouseName={warehouse?.name ?? session.warehouseId}
          locations={locations ?? []}
          products={products ?? []}
          isOwner={isOwner}
        />
      )}
    </main>
  );
}
