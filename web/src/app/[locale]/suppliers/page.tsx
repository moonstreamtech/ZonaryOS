// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchSuppliers } from "@/lib/inventory";
import SuppliersManager from "@/components/Suppliers/SuppliersManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Inventory management batch's supplier directory page. Member-gated
// read, owner-gated write (see internal/inventory's own package doc
// comment) - mirrors app/[locale]/inventory/page.tsx's own shape exactly.
export default async function SuppliersPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Suppliers");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const suppliers = await fetchSuppliers(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("title")}
        </h1>
      </div>

      {suppliers === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <SuppliersManager firmId={firm.firmId} suppliers={suppliers} isOwner={isOwner} />
      )}
    </main>
  );
}
