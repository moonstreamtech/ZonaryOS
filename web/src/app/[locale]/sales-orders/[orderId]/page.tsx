// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchSalesOrder } from "@/lib/salesorders";
import SalesOrderDetail from "@/components/SalesOrders/SalesOrderDetail";

type PageProps = {
  params: Promise<{ locale: string; orderId: string }>;
};

// Sales orders + full procurement cycle batch's order detail page -
// mirrors /invoices/[invoiceId]'s own shape. Member-gated read,
// owner-gated write (status changes), same tier as the /sales-orders list
// page itself.
export default async function SalesOrderDetailPage({ params }: PageProps) {
  const { locale, orderId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("SalesOrders");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const order = await fetchSalesOrder(sessionToken, firm.firmId, orderId);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {order === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <SalesOrderDetail firmId={firm.firmId} order={order} isOwner={isOwner} />
      )}
    </main>
  );
}
