// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchSalesOrders } from "@/lib/salesorders";
import { fetchCustomers } from "@/lib/crm";
import SalesOrdersManager from "@/components/SalesOrders/SalesOrdersManager";
import ListPageHeader from "@/components/ui/ListPageHeader";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Sales orders + full procurement cycle batch's sales order list: orders
// created either directly (owner-gated) or automatically by
// stock_to_sale's record_sale transition (the workflow-to-sales-order
// bridge, internal/workflow's TransitionSpec.Effects carrying a
// SalesOrderTemplate - see internal/salesorders' own package doc
// comment). Member-gated read, owner-gated write, same tier as
// /invoices.
export default async function SalesOrdersPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("SalesOrders");
  const tNav = await getTranslations("Nav");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [orders, customers] = await Promise.all([
    fetchSalesOrders(sessionToken, firm.firmId),
    fetchCustomers(sessionToken, firm.firmId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 px-6 py-10">
      <div className="w-full max-w-4xl">
        <ListPageHeader
          title={t("title")}
          breadcrumb={[{ label: tNav("brand"), href: "/" }, { label: t("title") }]}
        />
      </div>

      {orders === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <SalesOrdersManager firmId={firm.firmId} orders={orders} customers={customers ?? []} isOwner={isOwner} />
      )}
    </main>
  );
}
