// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchAIConfigs } from "@/lib/aiConfig";
import AiConfigManager from "@/components/Settings/AiConfigManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Vision §5's AI integration layer, configuration half (internal/ai): the
// only UI surface for Part 4 (anomaly detection) too - that part is a
// background check with no UI of its own, so this page is where a firm
// owner opts a firm into AI at all. Owner-gated the same tier as
// /settings/api-keys: configuring which external AI provider a firm's
// business data prompts get sent to is a structural firm decision, not
// an ordinary member action.
export default async function AiConfigPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("AiConfigSettings");

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

  const configs = await fetchAIConfigs(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-3xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
        <p className="max-w-2xl text-center text-sm text-zinc-600 dark:text-zinc-400">{t("description")}</p>
      </div>

      {configs === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <AiConfigManager firmId={firm.firmId} configs={configs} />
      )}
    </main>
  );
}
