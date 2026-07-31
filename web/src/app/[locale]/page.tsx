// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchMe, fetchRoleInFirm } from "@/lib/me";
import { resolveActiveFirm } from "@/lib/activeFirm";
import { fetchDefinitions, fetchInstanceCounts } from "@/lib/workflow";
import { Link } from "@/i18n/navigation";
import QuickCreatePanel from "@/components/Workflow/QuickCreatePanel";

type PageProps = {
  params: Promise<{ locale: string }>;
};

async function fetchBackendStatus(): Promise<boolean> {
  const apiBase = process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
  try {
    const res = await fetch(`${apiBase}/healthz`, { cache: "no-store" });
    return res.ok;
  } catch {
    return false;
  }
}

// Firm dashboard (item 2 of this batch): the caller's firm, their role in
// it, and a genuine cross-workflow overview - every workflow definition
// the firm has, each broken down by how many instances currently sit in
// each state ("Stock: 12 in stock, 3 sold" / "Customers: 5 leads, 2
// customers, 1 lost") - computed by one grouped backend query
// (internal/workflow.InstanceCountsByDefinition via
// lib/workflow.fetchInstanceCounts), not by fetching every instance and
// counting client-side. This replaces the old hardcoded single
// stock-item-count card, which only ever worked for one firm's one
// workflow and said nothing once a firm had a second definition (e.g.
// Customer Pipeline, seeded alongside Stock In -> Sale for every firm
// created from this batch on - see internal/wizard.CreateDefaultFirm).
//
// Server-rendered like every other firm-scoped page in this app: `counts
// === null` is the explicit error state (backend unreachable/RLS denied
// something unexpected), `counts.length === 0` is the explicit empty
// state (no workflow definitions yet - possible for a firm predating any
// seeding), otherwise one card per definition.
export default async function Home({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Home");
  const tAuth = await getTranslations("Auth");
  const tDash = await getTranslations("Dashboard");
  const backendUp = await fetchBackendStatus();

  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  const me = sessionToken ? await fetchMe(sessionToken) : null;

  // Vision §3's wizard trigger: a signed-in user with zero firm
  // memberships is routed into the wizard instead of the dashboard below.
  if (me && me.firms.length === 0) {
    redirect(`/${locale}/wizard`);
  }

  if (!me) {
    return (
      <main className="flex flex-1 flex-col items-center justify-center gap-6 bg-zinc-50 px-6 py-24 text-center dark:bg-black">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {t("title")}
        </h1>
        <p className="max-w-md text-lg text-zinc-600 dark:text-zinc-400">
          {t("subtitle")}
        </p>
        <p className="text-sm text-zinc-500 dark:text-zinc-500">
          {t("statusLabel")}:{" "}
          <span className={backendUp ? "text-green-600" : "text-red-600"}>
            {backendUp ? t("statusOk") : t("statusError")}
          </span>
        </p>
        {/* eslint-disable-next-line @next/next/no-html-link-for-pages -- /api/auth/login is a route handler, not a page */}
        <a
          href="/api/auth/login"
          data-permission-public="true"
          className="rounded-full bg-foreground px-5 py-2 text-sm font-medium text-background transition-colors hover:bg-[#383838] dark:hover:bg-[#ccc]"
        >
          {tAuth("signIn")}
        </a>
      </main>
    );
  }

  const firm = await resolveActiveFirm(me);
  const role = await fetchRoleInFirm(sessionToken!, firm.firmId);

  const [definitions, counts] = await Promise.all([
    fetchDefinitions(sessionToken!, firm.firmId),
    fetchInstanceCounts(sessionToken!, firm.firmId),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-8 bg-zinc-50 px-6 py-16 dark:bg-black">
      <div className="flex flex-col items-center gap-2 text-center">
        <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
          {firm.firmName}
        </h1>
        <p className="text-zinc-600 dark:text-zinc-400">
          {tDash("welcome", { name: me.displayName || me.email })}
        </p>
        <p className="text-sm text-zinc-500 dark:text-zinc-500">
          {tDash("roleLabel")}: {role?.roleName ?? tDash("roleUnavailable")}
        </p>
      </div>

      <div className="flex w-full max-w-2xl flex-col gap-3">
        <h2 className="self-start text-sm font-medium text-zinc-500 dark:text-zinc-400">
          {tDash("overviewTitle")}
        </h2>

        {counts === null ? (
          <p className="text-red-600 dark:text-red-400">{tDash("overviewLoadError")}</p>
        ) : counts.length === 0 ? (
          <p className="text-zinc-600 dark:text-zinc-400">{tDash("overviewEmpty")}</p>
        ) : (
          <div className="grid w-full gap-4 sm:grid-cols-2">
            {counts.map((definitionCounts) => (
              <Link
                key={definitionCounts.definitionId}
                href={`/workflows/${definitionCounts.key}`}
                data-permission-public="true"
                className="rounded-lg border border-zinc-200 bg-white p-5 text-left transition-colors hover:border-zinc-400 dark:border-zinc-800 dark:bg-zinc-950 dark:hover:border-zinc-600"
              >
                {/* definitionCounts.name/stateName are workflow_definitions
                    /workflow_states.name from the backend - data, not UI
                    copy, same convention as elsewhere in this component
                    tree. */}
                <h3 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">
                  {definitionCounts.name}
                </h3>
                <p className="mt-1 text-lg font-semibold text-black dark:text-zinc-50">
                  {definitionCounts.counts
                    .map((c) => `${c.count} ${c.stateName}`)
                    .join(", ")}
                </p>
              </Link>
            ))}
          </div>
        )}
      </div>

      <div className="w-full max-w-2xl">
        <h2 className="mb-3 text-sm font-medium text-zinc-500 dark:text-zinc-400">
          {tDash("quickCreateTitle")}
        </h2>
        <QuickCreatePanel firmId={firm.firmId} definitions={definitions ?? []} />
      </div>

      <div className="grid w-full max-w-2xl gap-4 sm:grid-cols-2">
        <div className="rounded-lg border border-zinc-200 bg-white p-5 dark:border-zinc-800 dark:bg-zinc-950">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">
            {tDash("workflowCountLabel")}
          </h2>
          <p className="mt-1 text-2xl font-semibold text-black dark:text-zinc-50">
            {definitions === null ? tDash("itemCountUnavailable") : definitions.length}
          </p>
          <Link
            href="/workflows"
            data-permission-public="true"
            className="mt-3 inline-block text-sm font-medium text-zinc-950 underline dark:text-zinc-50"
          >
            {tDash("goToWorkflows")}
          </Link>
        </div>

        {role?.isOwner && (
          <div className="rounded-lg border border-zinc-200 bg-white p-5 dark:border-zinc-800 dark:bg-zinc-950">
            <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">
              {tDash("auditLogCardTitle")}
            </h2>
            <p className="mt-1 text-sm text-zinc-600 dark:text-zinc-400">
              {tDash("auditLogCardBody")}
            </p>
            <Link
              href="/audit-log"
              data-permission-public="true"
              className="mt-3 inline-block text-sm font-medium text-zinc-950 underline dark:text-zinc-50"
            >
              {tDash("goToAuditLog")}
            </Link>
          </div>
        )}
      </div>

      <p className="text-xs text-zinc-500 dark:text-zinc-500">
        {t("statusLabel")}:{" "}
        <span className={backendUp ? "text-green-600" : "text-red-600"}>
          {backendUp ? t("statusOk") : t("statusError")}
        </span>
      </p>
    </main>
  );
}
