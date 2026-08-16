// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchAsset, fetchMaintenanceSchedules, fetchMaintenanceRecords } from "@/lib/asset";
import { fetchPeople } from "@/lib/hr";
import AssetDetail from "@/components/Asset/AssetDetail";

type PageProps = {
  params: Promise<{ locale: string; assetId: string }>;
};

// New internal/asset package's asset detail page (this batch): full
// asset fields, its maintenance schedule list (owner-gated create form),
// and maintenance record history (owner-gated log-maintenance form).
// Member-gated read, owner-gated write - same tier
// /inventory/warehouses/[warehouseId] uses.
export default async function AssetDetailPage({ params }: PageProps) {
  const { locale, assetId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Assets");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const asset = await fetchAsset(sessionToken, firm.firmId, assetId);
  const [schedules, records, people] = await Promise.all([
    asset ? fetchMaintenanceSchedules(sessionToken, firm.firmId, assetId) : Promise.resolve(null),
    asset ? fetchMaintenanceRecords(sessionToken, firm.firmId, assetId) : Promise.resolve(null),
    fetchPeople(sessionToken, firm.firmId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {asset === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <AssetDetail
          firmId={firm.firmId}
          asset={asset}
          schedules={schedules ?? []}
          records={records ?? []}
          people={people ?? []}
          isOwner={isOwner}
        />
      )}
    </main>
  );
}
