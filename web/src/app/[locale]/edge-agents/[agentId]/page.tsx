// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import { fetchEdgeAgent, fetchEdgeAgentTokens, fetchEdgeAgentEvents } from "@/lib/edgeAgents";
import EdgeAgentDetail from "@/components/EdgeAgents/EdgeAgentDetail";

type PageProps = {
  params: Promise<{ locale: string; agentId: string }>;
};

// Edge Agent detail page: event log plus token management (issue a new
// token, list existing tokens without ever showing the secret again -
// see internal/edgeagent.IssueToken's own doc comment). Member-gated
// read, owner-gated token management, same tier as the /edge-agents list
// page itself.
export default async function EdgeAgentDetailPage({ params }: PageProps) {
  const { locale, agentId } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("EdgeAgents");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const agent = await fetchEdgeAgent(sessionToken, firm.firmId, agentId);
  const [tokens, events] = await Promise.all([
    isOwner ? fetchEdgeAgentTokens(sessionToken, firm.firmId, agentId) : Promise.resolve([]),
    fetchEdgeAgentEvents(sessionToken, firm.firmId, agentId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      {agent === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <EdgeAgentDetail
          firmId={firm.firmId}
          agent={agent}
          tokens={tokens ?? []}
          events={events ?? []}
          isOwner={isOwner}
        />
      )}
    </main>
  );
}
