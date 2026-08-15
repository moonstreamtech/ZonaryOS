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
import { fetchAuditLogPage, type AuditLogEntry } from "@/lib/auditlog";
import { Link } from "@/i18n/navigation";
import QuickCreatePanel from "@/components/Workflow/QuickCreatePanel";
import Panel from "@/components/ui/Panel";

type PageProps = {
  params: Promise<{ locale: string }>;
};

const RECENT_ACTIVITY_LIMIT = 10;

async function fetchBackendStatus(): Promise<boolean> {
  const apiBase = process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
  try {
    const res = await fetch(`${apiBase}/healthz`, { cache: "no-store" });
    return res.ok;
  } catch {
    return false;
  }
}

// Trend glyph (up-right arrow / flat dash) - deliberately not a fake
// historical trend line: there's no time-series data to diff against, so
// this is scoped honestly to "does this workflow currently have any
// instances at all" per the batch brief (item 3), nothing more.
//
// `tone` (added this batch): the KPI cards below now have a dark header
// strip (zinc-900) with the light content area's cards elsewhere still
// light, so this glyph needs a colorway that reads on both - "light" for
// the dark strip's white-on-dark number row, "default" (the original
// zinc-400/green-500 pairing) for everywhere else.
function TrendGlyph({ up, tone = "default" }: { up: boolean; tone?: "default" | "light" }) {
  if (up) {
    return (
      <svg
        viewBox="0 0 16 16"
        className={tone === "light" ? "h-4 w-4 text-green-400" : "h-4 w-4 text-green-600 dark:text-green-500"}
        aria-hidden="true"
      >
        <path
          d="M2 13 L7 8 L10 11 L14 4"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <path d="M10 4 H14 V8" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    );
  }
  return (
    <svg
      viewBox="0 0 16 16"
      className={tone === "light" ? "h-4 w-4 text-zinc-500" : "h-4 w-4 text-zinc-400 dark:text-zinc-500"}
      aria-hidden="true"
    >
      <path d="M2 8 H14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  );
}

function WorkflowIcon({ tone = "default" }: { tone?: "default" | "light" }) {
  return (
    <svg
      viewBox="0 0 20 20"
      className={tone === "light" ? "h-5 w-5 text-zinc-400" : "h-5 w-5 text-zinc-500 dark:text-zinc-400"}
      aria-hidden="true"
    >
      <rect x="2" y="3" width="6" height="6" rx="1" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <rect x="12" y="11" width="6" height="6" rx="1" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <path d="M8 6 H12 V11" fill="none" stroke="currentColor" strokeWidth="1.4" />
    </svg>
  );
}

function AuditIcon() {
  return (
    <svg viewBox="0 0 20 20" className="h-5 w-5 text-zinc-400" aria-hidden="true">
      <path d="M4 3 H14 L16 5 V17 H4 Z" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
      <path d="M7 8 H13 M7 11 H13 M7 14 H10" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
    </svg>
  );
}

function ActivityIcon({ action }: { action: string }) {
  // Rough per-action-type shape (not semantically exhaustive - the batch
  // brief scopes this as "doesn't need to be semantically perfect"):
  // create gets a plus, delete gets a minus, everything else (update,
  // transition, approve, ...) gets a pulse/change glyph.
  if (action.includes("create")) {
    return (
      <svg viewBox="0 0 16 16" className="h-4 w-4 text-green-600 dark:text-green-500" aria-hidden="true">
        <path d="M8 2 V14 M2 8 H14" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
      </svg>
    );
  }
  if (action.includes("delete")) {
    return (
      <svg viewBox="0 0 16 16" className="h-4 w-4 text-red-600 dark:text-red-500" aria-hidden="true">
        <path d="M2 8 H14" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 16 16" className="h-4 w-4 text-zinc-500 dark:text-zinc-400" aria-hidden="true">
      <path d="M2 9 L5 9 L7 4 L9 13 L11 9 L14 9" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

async function fetchRecentActivity(
  token: string,
  firmId: string,
): Promise<AuditLogEntry[] | null> {
  const page = await fetchAuditLogPage(token, firmId, { limit: RECENT_ACTIVITY_LIMIT, offset: 0 });
  return page ? page.entries : null;
}

function FeatureWorkflowIcon() {
  return (
    <svg viewBox="0 0 20 20" className="h-6 w-6 text-zinc-500 dark:text-zinc-400" aria-hidden="true">
      <rect x="2" y="3" width="6" height="6" rx="1" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <rect x="12" y="11" width="6" height="6" rx="1" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <path d="M8 6 H12 V11" fill="none" stroke="currentColor" strokeWidth="1.4" />
    </svg>
  );
}

function FeaturePermissionIcon() {
  return (
    <svg viewBox="0 0 20 20" className="h-6 w-6 text-zinc-500 dark:text-zinc-400" aria-hidden="true">
      <path
        d="M10 2 L17 5 V10 C17 14 14 17 10 18 C6 17 3 14 3 10 V5 Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
      <path d="M7 10 L9 12 L13 7" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function FeatureAuditIcon() {
  return (
    <svg viewBox="0 0 20 20" className="h-6 w-6 text-zinc-500 dark:text-zinc-400" aria-hidden="true">
      <path d="M4 3 H14 L16 5 V17 H4 Z" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
      <path d="M7 8 H13 M7 11 H13 M7 14 H10" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" />
    </svg>
  );
}

// Public landing page (unauthenticated visitors only) - see docs task
// "Public landing page + registration flow". Dark zinc-900 hero mirrors
// the dashboard's own dark header-strip styling above, then a light
// features section and a plain-text footer. Every interactive element
// carries data-permission-public="true" since none of it is
// permission-gated (Vision §3 permission-tag rule applies to gated UI;
// public marketing chrome is tagged explicitly so the audit doesn't flag
// it as untagged-and-forgotten).
async function LandingPage() {
  const tLanding = await getTranslations("Landing");

  const features = [
    { icon: <FeatureWorkflowIcon />, title: tLanding("feature1Title"), body: tLanding("feature1Body") },
    { icon: <FeaturePermissionIcon />, title: tLanding("feature2Title"), body: tLanding("feature2Body") },
    { icon: <FeatureAuditIcon />, title: tLanding("feature3Title"), body: tLanding("feature3Body") },
  ];

  return (
    <main className="flex flex-1 flex-col">
      <section className="flex flex-col items-center gap-8 bg-zinc-900 px-6 py-24 text-center">
        <h1 className="text-5xl font-bold tracking-tight text-white sm:text-6xl">
          {tLanding("wordmark")}
        </h1>
        <p className="max-w-xl text-lg text-zinc-300 sm:text-xl">{tLanding("tagline")}</p>
        <div className="flex flex-col gap-3 sm:flex-row">
          {/* eslint-disable-next-line @next/next/no-html-link-for-pages -- /api/auth/login is a route handler, not a page */}
          <a
            href="/api/auth/login"
            data-permission-public="true"
            className="rounded-full border border-zinc-500 px-6 py-3 text-sm font-medium text-white transition-colors hover:bg-zinc-800"
          >
            {tLanding("signIn")}
          </a>
          {/* eslint-disable-next-line @next/next/no-html-link-for-pages -- /api/auth/login is a route handler, not a page */}
          <a
            href="/api/auth/login?mode=register"
            data-permission-public="true"
            className="rounded-full bg-white px-6 py-3 text-sm font-medium text-zinc-900 transition-colors hover:bg-zinc-200"
          >
            {tLanding("getStarted")}
          </a>
        </div>
      </section>

      <section className="grid gap-8 bg-white px-6 py-16 dark:bg-zinc-950 sm:grid-cols-3 sm:gap-6">
        {features.map((feature) => (
          <div key={feature.title} className="flex flex-col items-center gap-3 text-center sm:items-start sm:text-left">
            {feature.icon}
            <h2 className="text-base font-semibold text-black dark:text-zinc-50">{feature.title}</h2>
            <p className="text-sm text-zinc-600 dark:text-zinc-400">{feature.body}</p>
          </div>
        ))}
      </section>

      <footer className="flex flex-col items-center gap-2 border-t border-zinc-200 bg-zinc-50 px-6 py-8 text-center text-sm text-zinc-500 dark:border-zinc-800 dark:bg-black dark:text-zinc-500">
        <p>
          {tLanding("footerLicense")}{" "}
          <Link href="/docs" data-permission-public="true" className="underline">
            {tLanding("footerDocs")}
          </Link>
        </p>
      </footer>
    </main>
  );
}

// Firm dashboard (item 3 of this batch, KPI-restyled): the caller's
// firm, their role in it, and a genuine cross-workflow overview - every
// workflow definition the firm has, each broken down by how many
// instances currently sit in each state ("Stock: 12 in stock, 3 sold" /
// "Customers: 5 leads, 2 customers, 1 lost") - computed by one grouped
// backend query (internal/workflow.InstanceCountsByDefinition via
// lib/workflow.fetchInstanceCounts), not by fetching every instance and
// counting client-side. All prior data-fetching and MUST-KEEP text
// content (definition names, state counts) is unchanged - see
// scripts/e2e_smoke_test.sh, which asserts on this page's raw HTML.
//
// New this batch: a recent-activity timeline (last 10 audit log entries,
// reusing lib/auditlog.ts's fetchAuditLogPage - the same helper
// app/[locale]/audit-log/page.tsx already uses - rather than a new fetch
// path), owner-gated the same way the audit log card already was and the
// /audit-log page itself is (internal/auditlog.ReadPermission is held by
// the owner role by default, see internal/wizard.CreateDefaultFirm).
export default async function Home({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Home");
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
    return <LandingPage />;
  }

  const firm = await resolveActiveFirm(me);
  const role = await fetchRoleInFirm(sessionToken!, firm.firmId);
  const isOwner = role?.isOwner ?? false;

  const [definitions, counts, recentActivity] = await Promise.all([
    fetchDefinitions(sessionToken!, firm.firmId),
    fetchInstanceCounts(sessionToken!, firm.firmId),
    isOwner ? fetchRecentActivity(sessionToken!, firm.firmId) : Promise.resolve(null),
  ]);

  return (
    <main className="flex flex-1 flex-col items-center gap-10 px-6 py-10">
      <div className="flex w-full max-w-5xl flex-col gap-1">
        {/* Dashboard typography (this batch): the greeting is now the
            small, muted line and the firm name is the big, bold,
            wordmark-like one - inverted from the prior batch's plain
            <h1>. tDash("welcome") itself is unchanged (still the single,
            non-time-of-day-aware string) - only its size/weight moved,
            per the task brief's explicit "restyle it smaller/muted, that
            alone satisfies the visual intent" fallback. */}
        <p className="text-sm text-zinc-500 dark:text-zinc-500">
          {tDash("welcome", { name: me.displayName || me.email })}
        </p>
        <h1 className="text-4xl font-bold tracking-tight text-black dark:text-zinc-50">
          {firm.firmName}
        </h1>
        <p className="text-sm text-zinc-500 dark:text-zinc-500">
          {tDash("roleLabel")}: {role?.roleName ?? tDash("roleUnavailable")}
        </p>
      </div>

      {/* Quick create - moved into a prominent card near the top of the
          dashboard per item 3 (a FAB was considered scope creep for this
          batch - see the task brief's own "your call" on this point). */}
      <div className="w-full max-w-5xl">
        <Panel className="!bg-zinc-50 dark:!bg-zinc-950">
          <h2 className="mb-3 text-sm font-medium text-zinc-500 dark:text-zinc-400">
            {tDash("quickCreateTitle")}
          </h2>
          <QuickCreatePanel firmId={firm.firmId} definitions={definitions ?? []} />
        </Panel>
      </div>

      {/* Workflow overview KPI group */}
      <div className="flex w-full max-w-5xl flex-col gap-3">
        <div className="flex items-center gap-3">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">
            {tDash("overviewTitle")}
          </h2>
          <div className="h-px flex-1 bg-zinc-200 dark:bg-zinc-800" />
        </div>

        {counts === null ? (
          <p className="text-red-600 dark:text-red-400">{tDash("overviewLoadError")}</p>
        ) : counts.length === 0 ? (
          <p className="text-zinc-600 dark:text-zinc-400">{tDash("overviewEmpty")}</p>
        ) : (
          <div className="grid w-full gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {counts.map((definitionCounts) => {
              const primaryCount = definitionCounts.counts.reduce((sum, c) => sum + c.count, 0);
              return (
                /* KPI card restyle (this batch): a darker zinc-900 header
                   strip carrying the icon/trend row + the big white
                   number, with the label + state breakdown in a lighter
                   panel below - per the design brief's "dark card header
                   strip, white number on dark, description below in
                   gray". `!p-0` overrides .panel's own padding (see
                   tokens.css) so the header strip can bleed edge-to-edge;
                   `overflow-hidden` keeps it inside the card's rounded
                   corners. */
                <Link
                  key={definitionCounts.definitionId}
                  href={`/workflows/${definitionCounts.key}`}
                  data-permission-public="true"
                  className="panel panel--interactive flex flex-col overflow-hidden !p-0"
                >
                  <div className="flex flex-col gap-2 bg-zinc-900 px-5 py-4">
                    <div className="flex items-center justify-between">
                      <WorkflowIcon tone="light" />
                      <TrendGlyph up={primaryCount > 0} tone="light" />
                    </div>
                    <p className="text-2xl font-bold text-white">{primaryCount}</p>
                  </div>
                  <div className="flex flex-col gap-1 px-5 py-3">
                    {/* definitionCounts.name/stateName are workflow_definitions
                        /workflow_states.name from the backend - data, not UI
                        copy, same convention as elsewhere in this component
                        tree. */}
                    <h3 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">
                      {definitionCounts.name}
                    </h3>
                    <p className="text-xs text-zinc-500 dark:text-zinc-500">
                      {definitionCounts.counts
                        .map((c) => `${c.count} ${c.stateName}`)
                        .join(", ")}
                    </p>
                  </div>
                </Link>
              );
            })}
          </div>
        )}
      </div>

      {/* Summary KPI group: workflow definition count + (owner-only)
          audit log entry point - the two cards that predate this batch's
          per-definition overview above, kept as their own small group
          rather than forced into the per-definition grid. */}
      <div className="flex w-full max-w-5xl flex-col gap-3">
        <div className="flex items-center gap-3">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">
            {tDash("summaryTitle")}
          </h2>
          <div className="h-px flex-1 bg-zinc-200 dark:bg-zinc-800" />
        </div>
        <div className="grid w-full gap-4 sm:grid-cols-2">
          <div className="panel flex flex-col overflow-hidden !p-0">
            <div className="flex flex-col gap-2 bg-zinc-900 px-5 py-4">
              <div className="flex items-center justify-between">
                <WorkflowIcon tone="light" />
                <TrendGlyph up={(definitions?.length ?? 0) > 0} tone="light" />
              </div>
              <p className="text-2xl font-bold text-white">
                {definitions === null ? tDash("itemCountUnavailable") : definitions.length}
              </p>
            </div>
            <div className="flex flex-col gap-1 px-5 py-3">
              <h3 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">
                {tDash("workflowCountLabel")}
              </h3>
              <Link
                href="/workflows"
                data-permission-public="true"
                className="text-sm font-medium text-zinc-950 underline dark:text-zinc-50"
              >
                {tDash("goToWorkflows")}
              </Link>
            </div>
          </div>

          {isOwner && (
            <div className="panel flex flex-col overflow-hidden !p-0">
              <div className="flex flex-col gap-2 bg-zinc-900 px-5 py-4">
                <AuditIcon />
              </div>
              <div className="flex flex-col gap-1 px-5 py-3">
                <h3 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">
                  {tDash("auditLogCardTitle")}
                </h3>
                <p className="text-sm text-zinc-600 dark:text-zinc-400">
                  {tDash("auditLogCardBody")}
                </p>
                <Link
                  href="/audit-log"
                  data-permission-public="true"
                  className="text-sm font-medium text-zinc-950 underline dark:text-zinc-50"
                >
                  {tDash("goToAuditLog")}
                </Link>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Recent activity timeline (new this batch, item 3) - owner-gated
          the same way the audit log card above is, since
          internal/auditlog.ReadPermission is owner-only by default. */}
      {isOwner && (
        <div className="flex w-full max-w-5xl flex-col gap-3">
          <div className="flex items-center gap-3">
            <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">
              {tDash("recentActivityTitle")}
            </h2>
            <div className="h-px flex-1 bg-zinc-200 dark:bg-zinc-800" />
          </div>
          <div className="panel">
            {recentActivity === null ? (
              <p className="text-red-600 dark:text-red-400">{tDash("overviewLoadError")}</p>
            ) : recentActivity.length === 0 ? (
              <p className="text-zinc-600 dark:text-zinc-400">{tDash("recentActivityEmpty")}</p>
            ) : (
              <ul className="flex flex-col gap-3">
                {recentActivity.map((entry) => (
                  <li key={entry.id} className="flex items-start gap-3 text-sm">
                    <span className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-zinc-200 dark:border-zinc-800">
                      <ActivityIcon action={entry.action} />
                    </span>
                    <div className="flex flex-1 flex-col">
                      <span className="text-black dark:text-zinc-50">
                        {/* entry.action/entityType are internal/auditlog
                            data, not UI copy - same convention as the audit
                            log page itself (AuditLogTable). */}
                        {entry.action} · {entry.entityType}
                      </span>
                      <span className="text-xs text-zinc-500 dark:text-zinc-500">
                        {new Date(entry.occurredAt).toLocaleString()}
                      </span>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}

      <p className="text-xs text-zinc-500 dark:text-zinc-500">
        {t("statusLabel")}:{" "}
        <span className={backendUp ? "text-green-600" : "text-red-600"}>
          {backendUp ? t("statusOk") : t("statusError")}
        </span>
      </p>
    </main>
  );
}
