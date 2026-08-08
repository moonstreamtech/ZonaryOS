// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchCustomers } from "@/lib/crm";
import CustomersManager from "@/components/Customers/CustomersManager";

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

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const customers = await fetchCustomers(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
      </div>

      {customers === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <CustomersManager firmId={firm.firmId} customers={customers} isOwner={isOwner} />
      )}
    </main>
  );
}
