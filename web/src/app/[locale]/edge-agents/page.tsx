// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchEdgeAgents } from "@/lib/edgeAgents";
import EdgeAgentsManager from "@/components/EdgeAgents/EdgeAgentsManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Vision §9's Edge Agent protocol foundation (internal/edgeagent): the
// firm's own list of registered Edge Agents, with status/last-seen/
// capabilities. Member-gated read, owner-gated write (registering a new
// agent), same tier as /logistics - this page renders for any member,
// but EdgeAgentsManager only shows the create control when isOwner.
export default async function EdgeAgentsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("EdgeAgents");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const agents = await fetchEdgeAgents(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
      </div>

      {agents === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <EdgeAgentsManager firmId={firm.firmId} agents={agents} isOwner={isOwner} />
      )}
    </main>
  );
}
