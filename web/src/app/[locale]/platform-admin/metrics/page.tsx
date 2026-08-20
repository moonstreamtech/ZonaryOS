// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { notFound } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchPlatformAdminFirms } from "@/lib/platformAdmin";
import { fetchMetricsSummary } from "@/lib/perfMetrics";
import MetricsDashboard from "@/components/PlatformAdmin/MetricsDashboard";
import PlatformAdminNav from "@/components/PlatformAdmin/PlatformAdminNav";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Performance monitoring's platform-admin dashboard (this batch,
// GET /api/platform-admin/metrics/summary) - a fixed last-24h window
// covering per-endpoint p50/p95/p99 response times, error rate, the top
// 10 slowest endpoints, and request volume by hour. Platform-wide, not
// firm-scoped, so this lives under /platform-admin next to firm-groups
// and exchange-rates. Gating mirrors those pages' own pattern exactly:
// re-uses fetchPlatformAdminFirms purely as the allowlist probe (the same
// internal/platformadmin.Allowlist gates every /metrics endpoint), a
// failed/non-2xx response there already meaning "this caller shouldn't
// see this feature exist at all" - notFound(), not an inline "not
// authorized" message, matching the backend's own 404-not-403 posture.
export default async function MetricsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("PlatformAdminMetrics");
  const tAdmin = await getTranslations("PlatformAdmin");

  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  if (!sessionToken) {
    notFound();
  }

  const allowlistProbe = await fetchPlatformAdminFirms(sessionToken);
  if (allowlistProbe === null) {
    notFound();
  }

  const summary = await fetchMetricsSummary(sessionToken);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-[var(--color-platform-accent)] px-6 py-16">
      {/* Same deliberately-not-zinc indigo accent as /platform-admin
          itself - see that page's own doc comment on why this section
          needs a visually unmistakable identity. */}
      <div className="flex w-full max-w-4xl flex-col items-center gap-3">
        <span className="rounded-full bg-[var(--color-platform-accent-strong)] px-3 py-1 text-xs font-semibold tracking-wide text-[var(--color-platform-on-accent)] uppercase">
          {tAdmin("badge")}
        </span>
        <h1 className="text-3xl font-semibold tracking-tight text-[var(--color-platform-on-accent)]">
          {t("title")}
        </h1>
        <p className="max-w-2xl text-center text-sm text-indigo-200">{t("description")}</p>
      </div>

      <PlatformAdminNav locale={locale} active="metrics" />

      {summary === null ? (
        <p className="text-red-300">{t("loadError")}</p>
      ) : (
        <MetricsDashboard summary={summary} />
      )}
    </main>
  );
}
