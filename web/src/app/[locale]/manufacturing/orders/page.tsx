// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchProductionOrders, fetchBOMs } from "@/lib/manufacturing";
import { fetchProducts } from "@/lib/inventory";
import ProductionOrdersManager from "@/components/Manufacturing/ProductionOrdersManager";
import ListPageHeader from "@/components/ui/ListPageHeader";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Manufacturing module foundation batch's production order list: orders
// that consume a BOM's components (internal/manufacturing's own
// Start/Complete/Cancel lifecycle). Member-gated read, owner-gated write.
export default async function ProductionOrdersPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Manufacturing");
  const tNav = await getTranslations("Nav");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [orders, boms, products] = await Promise.all([
    fetchProductionOrders(sessionToken, firm.firmId),
    fetchBOMs(sessionToken, firm.firmId),
    fetchProducts(sessionToken, firm.firmId, { activeOnly: true }),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 px-6 py-10">
      <div className="w-full max-w-4xl">
        <ListPageHeader
          title={t("ordersTitle")}
          breadcrumb={[{ label: tNav("brand"), href: "/" }, { label: t("ordersTitle") }]}
        />
      </div>

      {orders === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <ProductionOrdersManager
          firmId={firm.firmId}
          orders={orders}
          boms={boms ?? []}
          products={products ?? []}
          isOwner={isOwner}
        />
      )}
    </main>
  );
}
