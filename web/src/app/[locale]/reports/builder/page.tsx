// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { setRequestLocale, getTranslations } from "next-intl/server";
import { requireFirmContext } from "@/lib/firmContext";
import { fetchRoleInFirm } from "@/lib/me";
import ReportBuilder from "@/components/Reports/ReportBuilder";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Vision §3's reporting engine, builder UI: pick an entity, filters,
// grouping, metrics, and a date range, see results - a report builder,
// not a SQL editor (internal/reports.QuerySpec is the wire format, never
// raw SQL, see that package's own doc comment). Member-gated, same tier
// as /reports itself - defining a report over data the caller can
// already see is ordinary read access, not a structural firm decision.
//
// isOwner is fetched here (not just for /settings/* pages) because the
// AI integration layer's "Ask AI" panel on ReportBuilder itself calls
// the owner-gated suggest-report endpoint (internal/ai.SuggestReport) -
// ReportBuilder only renders that panel when isOwner is true, mirroring
// how DefinitionBuilder.tsx's own "Suggest with AI" panel is reachable
// only because that whole builder is already owner-gated.
export default async function ReportBuilderPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Reports");

  const { sessionToken, firm } = await requireFirmContext(locale);
  const role = await fetchRoleInFirm(sessionToken, firm.firmId);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("builderTitle")}
      </h1>
      <ReportBuilder firmId={firm.firmId} isOwner={role?.isOwner ?? false} />
    </main>
  );
}
