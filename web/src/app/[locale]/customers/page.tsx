// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchCustomers } from "@/lib/crm";
import CustomersManager from "@/components/Customers/CustomersManager";
import ListPageHeader from "@/components/ui/ListPageHeader";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Logistics/CRM batch's customer directory: customers created either
// directly (owner-gated) or automatically by customer_pipeline's
// "convert" transition (the workflow-to-CRM bridge, internal/workflow's
// TransitionSpec.Customer - see internal/crm's own package doc comment).
// Member-gated read, owner-gated write, same tier as /inventory.
export default async function CustomersPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Customers");
  const tNav = await getTranslations("Nav");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const customers = await fetchCustomers(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 px-6 py-10">
      <div className="w-full max-w-4xl">
        <ListPageHeader
          title={t("title")}
          breadcrumb={[{ label: tNav("brand"), href: "/" }, { label: t("title") }]}
        />
      </div>

      {customers === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <CustomersManager firmId={firm.firmId} customers={customers} isOwner={isOwner} />
      )}
    </main>
  );
}
