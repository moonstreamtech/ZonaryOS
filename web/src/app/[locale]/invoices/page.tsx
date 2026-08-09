// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchInvoices } from "@/lib/invoicing";
import { fetchCustomers } from "@/lib/crm";
import InvoicesManager from "@/components/Invoicing/InvoicesManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Invoicing/payment tracking batch's invoice list: invoices created
// either directly (owner-gated) or automatically by stock_to_sale's
// record_sale transition (the workflow-to-invoicing bridge,
// internal/workflow's TransitionSpec.Invoice - see internal/invoicing's
// own package doc comment). Member-gated read, owner-gated write, same
// tier as /customers/#/logistics.
export default async function InvoicesPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Invoices");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [invoices, customers] = await Promise.all([
    fetchInvoices(sessionToken, firm.firmId),
    fetchCustomers(sessionToken, firm.firmId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
      </div>

      {invoices === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <InvoicesManager firmId={firm.firmId} invoices={invoices} customers={customers ?? []} isOwner={isOwner} />
      )}
    </main>
  );
}
