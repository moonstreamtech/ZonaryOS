// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchProductionOrder } from "@/lib/manufacturing";
import { fetchProducts } from "@/lib/inventory";
import ProductionOrderDetail from "@/components/Manufacturing/ProductionOrderDetail";

type PageProps = {
  params: Promise<{ locale: string; orderId: string }>;
};

// Manufacturing module foundation batch's production order detail page:
// the material issue log and start/complete/cancel actions. Member-gated
// read, owner-gated write.
export default async function ProductionOrderDetailPage({ params }: PageProps) {
  const { locale, orderId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Manufacturing");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [order, products] = await Promise.all([
    fetchProductionOrder(sessionToken, firm.firmId, orderId),
    fetchProducts(sessionToken, firm.firmId, { activeOnly: true }),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {order === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <ProductionOrderDetail firmId={firm.firmId} order={order} products={products ?? []} isOwner={isOwner} />
      )}
    </main>
  );
}
