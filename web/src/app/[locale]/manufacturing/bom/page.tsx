// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchBOMs } from "@/lib/manufacturing";
import { fetchProducts } from "@/lib/inventory";
import BOMManager from "@/components/Manufacturing/BOMManager";
import ListPageHeader from "@/components/ui/ListPageHeader";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Manufacturing module foundation batch's BOM list: a product's Bill of
// Materials, with version management (only one version active per
// product at a time - internal/manufacturing.CreateBOM/UpdateBOM enforce
// this server-side). Member-gated read, owner-gated write, same tier
// /sales-orders and /invoices use.
export default async function BOMPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Manufacturing");
  const tNav = await getTranslations("Nav");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [boms, products] = await Promise.all([
    fetchBOMs(sessionToken, firm.firmId),
    fetchProducts(sessionToken, firm.firmId, { activeOnly: true }),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 px-6 py-10">
      <div className="w-full max-w-4xl">
        <ListPageHeader
          title={t("bomTitle")}
          breadcrumb={[{ label: tNav("brand"), href: "/" }, { label: t("bomTitle") }]}
        />
      </div>

      {boms === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <BOMManager firmId={firm.firmId} boms={boms} products={products ?? []} isOwner={isOwner} />
      )}
    </main>
  );
}
