// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { notFound } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchPlatformAdminFirms } from "@/lib/platformAdmin";
import { fetchAlertRules } from "@/lib/alertRules";
import { fetchAlerts } from "@/lib/alerts";
import AlertRulesManager from "@/components/PlatformAdmin/AlertRulesManager";
import PlatformAdminNav from "@/components/PlatformAdmin/PlatformAdminNav";

type PageProps = {
  params: Promise<{ locale: string }>;
};

// Performance monitoring's alerting management page (this batch,
// GET/POST /api/platform-admin/alert-rules, PATCH/DELETE .../{ruleId},
// and GET /api/platform-admin/alerts) - deterministic threshold rules on
// error_rate/response_time_p95/volume_drop, plus the recent alert events
// they've fired. Platform-wide, not firm-scoped, so this lives under
// /platform-admin next to firm-groups/exchange-rates/metrics. Gating
// mirrors those pages' own pattern exactly: re-uses
// fetchPlatformAdminFirms purely as the allowlist probe (the same
// internal/platformadmin.Allowlist gates every /alert-rules and /alerts
// endpoint) - notFound(), not an inline "not authorized" message,
// matching the backend's own 404-not-403 posture.
export default async function AlertsPage({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("PlatformAdminAlerts");
  const tAdmin = await getTranslations("PlatformAdmin");

  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  if (!sessionToken) {
    notFound();
  }

  const allowlistProbe = await fetchPlatformAdminFirms(sessionToken);
  if (allowlistProbe === null) {
    notFound();
  }

  const rules = await fetchAlertRules(sessionToken);
  const events = await fetchAlerts(sessionToken);

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

      <PlatformAdminNav locale={locale} active="alerts" />

      {rules === null || events === null ? (
        <p className="text-red-300">{t("loadError")}</p>
      ) : (
        <div className="w-full max-w-4xl rounded-lg border border-indigo-800 bg-white p-4 dark:bg-zinc-950">
          <AlertRulesManager rules={rules} events={events} />
        </div>
      )}
    </main>
  );
}
