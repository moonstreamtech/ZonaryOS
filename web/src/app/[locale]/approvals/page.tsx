// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchPendingApprovals } from "@/lib/approvals";
import ApprovalsManager from "@/components/Approvals/ApprovalsManager";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Approval workflow's own page (Open Points item 41): every pending
// approval the caller could actually resolve (holds the proposed
// action's own permission key - internal/workflow.ListPendingApprovals's
// own doc comment), with context (which instance, which action, who
// requested it, when it expires) and Approve/Reject buttons. There is no
// separate owner-gate here beyond ListPendingApprovals' own permission
// filtering - the backend already only returns what this caller could
// act on.
export default async function ApprovalsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Approvals");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const approvals = await fetchPendingApprovals(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex w-full max-w-4xl flex-col items-center gap-2">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">{t("title")}</h1>
      </div>

      {approvals === null ? (
        <p className="text-red-600 dark:text-red-400">{t("loadError")}</p>
      ) : (
        <ApprovalsManager firmId={firm.firmId} approvals={approvals} />
      )}
    </main>
  );
}
