// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchConnectors } from "@/lib/integrations";
import IntegrationsManager from "@/components/Settings/IntegrationsManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Data pipeline / ETL / system integrations, frontend side
// (internal/integration): connectors + field mappings + sync history for
// pushing/pulling data to/from an external system. Unlike
// /settings/webhooks and /settings/ai-config (whole page owner-gated),
// this page itself is member-visible - ListConnectors/ListMappings/
// ListSyncLogs are all member-gated reads on the Go side (see
// internal/integration/connectors.go's own doc comment on that split);
// only the create/edit/delete actions are owner-gated, so
// IntegrationsManager itself decides per-element whether to render them,
// driven by the `isOwner` prop below.
export default async function IntegrationsSettingsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("IntegrationsSettings");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);

  const connectors = await fetchConnectors(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-3xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
        <p className="max-w-2xl text-center text-sm text-zinc-600 dark:text-zinc-400">{t("description")}</p>
        <p className="max-w-2xl text-center text-xs text-zinc-500 dark:text-zinc-500">{t("scopeNote")}</p>
      </div>

      {connectors === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <IntegrationsManager firmId={firm.firmId} connectors={connectors} isOwner={role?.isOwner ?? false} />
      )}
    </main>
  );
}
