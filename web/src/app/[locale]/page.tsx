// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchMe, fetchRoleInFirm } from "@/lib/me";
import { resolveActiveFirm } from "@/lib/activeFirm";
import {
  fetchDefinitionByKey,
  fetchDefinitions,
  fetchInstances,
  STOCK_TO_SALE_KEY,
} from "@/lib/workflow";
import { Link } from "@/i18n/navigation";

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

// Firm dashboard (item 2): the caller's firm, their role in it, and a
// quick summary drawn from data the backend already exposes - no new
// backend surface invented for this. In-stock item count comes from the
// same Stock In -> Sale workflow instances the stock page lists (see
// lib/workflow.ts); "in stock" is anything not yet in the workflow's
// terminal "sold" state. definition === null (the workflow was never
// seeded for this firm - possible for a firm created before PR 8) is
// rendered as an explicit "not set up" summary state, not an error.
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

  const definition = await fetchDefinitionByKey(
    sessionToken!,
    firm.firmId,
    STOCK_TO_SALE_KEY,
  );
  const instances = definition
    ? await fetchInstances(sessionToken!, firm.firmId, definition.definitionId)
    : null;
  const inStockCount =
    instances?.filter((i) => i.state.key !== "sold").length ?? null;
  const definitions = await fetchDefinitions(sessionToken!, firm.firmId);

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

      <div className="grid w-full max-w-2xl gap-4 sm:grid-cols-2">
        <div className="rounded-lg border border-zinc-200 bg-white p-5 dark:border-zinc-800 dark:bg-zinc-950">
          <h2 className="text-sm font-medium text-zinc-500 dark:text-zinc-400">
            {tDash("itemCountLabel")}
          </h2>
          <p className="mt-1 text-2xl font-semibold text-black dark:text-zinc-50">
            {inStockCount === null ? tDash("itemCountUnavailable") : inStockCount}
          </p>
          <Link
            href="/stock"
            data-permission-public="true"
            className="mt-3 inline-block text-sm font-medium text-zinc-950 underline dark:text-zinc-50"
          >
            {tDash("goToStock")}
          </Link>
        </div>

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
