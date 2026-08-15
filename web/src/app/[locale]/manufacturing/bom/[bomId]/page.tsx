// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchBOM, fetchBOMs } from "@/lib/manufacturing";
import { fetchProducts } from "@/lib/inventory";
import BOMDetail from "@/components/Manufacturing/BOMDetail";

type PageProps = {
  params: Promise<{ locale: string; bomId: string }>;
};

// Manufacturing module foundation batch's BOM detail page: components,
// and version management - every OTHER version for the same product
// (fetchBOMs filtered by productId), so the owner can switch which one is
// active from here. Member-gated read, owner-gated write.
export default async function BOMDetailPage({ params }: PageProps) {
  const { locale, bomId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Manufacturing");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const bom = await fetchBOM(sessionToken, firm.firmId, bomId);
  const [otherVersions, products] = await Promise.all([
    bom ? fetchBOMs(sessionToken, firm.firmId, { productId: bom.productId }) : Promise.resolve(null),
    fetchProducts(sessionToken, firm.firmId, { activeOnly: true }),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {bom === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <BOMDetail
          firmId={firm.firmId}
          bom={bom}
          otherVersions={(otherVersions ?? []).filter((v) => v.id !== bom.id)}
          products={products ?? []}
          isOwner={isOwner}
        />
      )}
    </main>
  );
}
