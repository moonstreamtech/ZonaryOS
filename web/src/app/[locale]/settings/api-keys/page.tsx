// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchAPIKeys } from "@/lib/apikeys";
import { fetchFirmPermissionAudit } from "@/lib/permissionAudit";
import ApiKeysManager from "@/components/Settings/ApiKeysManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// API keys foundation (this batch, internal/apikey): non-interactive
// programmatic access, scoped to a subset of the creating owner's own
// permissions. The create form's scope checkboxes are drawn from
// fetchFirmPermissionAudit's own myPermissionKeys - only permission keys
// the signed-in owner actually holds, so the form can't even offer a
// scope the backend would reject anyway (CreateAPIKey re-validates this
// server-side regardless - this is a UX convenience, not the real
// safety boundary). Owner-gated, same tier as /settings/document-templates.
export default async function ApiKeysSettingsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("ApiKeysSettings");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);

  if (!role?.isOwner) {
    return (
      <main className="flex flex-1 flex-col items-center justify-center gap-4 bg-zinc-50 px-6 py-24 text-center dark:bg-black">
        <h1 className="text-2xl font-semibold text-black dark:text-zinc-50">{t("title")}</h1>
        <p className="text-red-600 dark:text-red-400">{t("notAuthorized")}</p>
      </main>
    );
  }

  const [keys, audit] = await Promise.all([
    fetchAPIKeys(sessionToken, firm.firmId),
    fetchFirmPermissionAudit(sessionToken, firm.firmId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-3xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
        <p className="max-w-2xl text-center text-sm text-zinc-600 dark:text-zinc-400">{t("description")}</p>
      </div>

      {keys === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <ApiKeysManager firmId={firm.firmId} apiKeys={keys} availableScopes={audit?.myPermissionKeys ?? []} />
      )}
    </main>
  );
}
