// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchWarehouses } from "@/lib/warehouse";
import WarehousesManager from "@/components/Warehouse/WarehousesManager";
import ListPageHeader from "@/components/ui/ListPageHeader";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Warehouse management batch's warehouse list: named storage sites for a
// firm (internal/warehouse.Warehouse), each with its own location tree and
// stock. Member-gated read, owner-gated write - same tier
// /manufacturing/bom and /manufacturing/orders use.
export default async function WarehousesPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Warehouse");
  const tNav = await getTranslations("Nav");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const warehouses = await fetchWarehouses(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 px-6 py-10">
      <div className="w-full max-w-4xl">
        <ListPageHeader
          title={t("warehousesTitle")}
          breadcrumb={[{ label: tNav("brand"), href: "/" }, { label: t("warehousesTitle") }]}
        />
      </div>

      {warehouses === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <WarehousesManager firmId={firm.firmId} warehouses={warehouses} isOwner={isOwner} />
      )}
    </main>
  );
}
