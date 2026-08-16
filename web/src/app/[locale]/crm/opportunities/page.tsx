// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchOpportunities, fetchCustomers } from "@/lib/crm";
import OpportunitiesManager from "@/components/Crm/OpportunitiesManager";
import ListPageHeader from "@/components/ui/ListPageHeader";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// The CRM opportunities pipeline (this batch): every open/won/lost deal
// for the firm, either as a stage-column kanban (CSS-only, no
// drag-and-drop library per the batch brief) or a plain table - the
// choice is a client-only localStorage preference (OpportunitiesManager),
// not a DB column. Member-gated read, owner-gated create/stage-change,
// same tier as /customers.
export default async function OpportunitiesPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Crm");
  const tNav = await getTranslations("Nav");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [opportunities, customers] = await Promise.all([
    fetchOpportunities(sessionToken, firm.firmId),
    fetchCustomers(sessionToken, firm.firmId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 px-6 py-10">
      <div className="w-full max-w-5xl">
        <ListPageHeader
          title={t("opportunitiesTitle")}
          breadcrumb={[{ label: tNav("brand"), href: "/" }, { label: t("opportunitiesTitle") }]}
        />
      </div>

      {opportunities === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <OpportunitiesManager
          firmId={firm.firmId}
          opportunities={opportunities}
          customers={customers ?? []}
          isOwner={isOwner}
        />
      )}
    </main>
  );
}
