// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchInvoice, fetchPayments } from "@/lib/invoicing";
import InvoiceDetail from "@/components/Invoicing/InvoiceDetail";

type PageProps = {
  params: Promise<{ locale: string; invoiceId: string }>;
};

// Invoicing/payment tracking batch's invoice detail page - "click-through
// to invoice detail with lines and payments" (design brief). Member-gated
// read, owner-gated write (status changes, recording a payment), same
// tier as the /invoices list page itself.
export default async function InvoiceDetailPage({ params }: PageProps) {
  const { locale, invoiceId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Invoices");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [invoice, payments] = await Promise.all([
    fetchInvoice(sessionToken, firm.firmId, invoiceId),
    fetchPayments(sessionToken, firm.firmId, invoiceId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {invoice === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <InvoiceDetail firmId={firm.firmId} invoice={invoice} payments={payments ?? []} isOwner={isOwner} />
      )}
    </main>
  );
}
