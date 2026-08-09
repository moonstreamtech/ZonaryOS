// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations } from "next-intl/server";
import { Link } from "@/i18n/navigation";
import type { MeResponse } from "@/lib/me";
import FirmSwitcher from "./FirmSwitcher";
import GlobalSearchBox from "./GlobalSearchBox";
import AuditModeClient from "@/components/AuditMode/AuditModeClient";

type Props = {
  me: MeResponse | null;
  activeFirmId: string | null;
  isOwner: boolean;
};

// Persistent navigation shell, mounted once in the root layout so it's
// present on every authenticated page (item 5) - replacing the prior
// single-link-on-homepage pattern (see app/[locale]/page.tsx's old
// "goToStock" link) with a real header: brand, firm switcher, section
// links, and Logout. Renders nothing for a signed-out visitor - the
// homepage still owns its own sign-in call to action.
//
// The Audit Mode toggle (components/AuditMode/AuditModeClient.tsx) is
// rendered from here rather than directly in the root layout - it was
// the layout's only other piece of authenticated-session UI, floating
// unanchored to any structure; this shell is now the one place that owns
// "chrome shown on every authenticated page."
export default async function NavShell({ me, activeFirmId, isOwner }: Props) {
  const t = await getTranslations("Nav");

  if (!me) {
    return <AuditModeClient firmId={activeFirmId} isOwner={isOwner} />;
  }

  return (
    <>
      <header className="flex flex-wrap items-center justify-between gap-3 border-b border-zinc-200 bg-white px-6 py-3 dark:border-zinc-800 dark:bg-black">
        <div className="flex flex-wrap items-center gap-4">
          <Link
            href="/"
            data-permission-public="true"
            className="text-sm font-semibold tracking-tight text-black dark:text-zinc-50"
          >
            {t("brand")}
          </Link>

          {activeFirmId && (
            <FirmSwitcher firms={me.firms} activeFirmId={activeFirmId} />
          )}

          {activeFirmId && (
            <nav className="flex items-center gap-3 text-sm">
              <Link
                href="/stock"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("stock")}
              </Link>
              <Link
                href="/workflows"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("workflows")}
              </Link>
              <Link
                href="/settings"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("settings")}
              </Link>
              <Link
                href="/financials"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("financials")}
              </Link>
              <Link
                href="/hr"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("hr")}
              </Link>
              <Link
                href="/tasks"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("tasks")}
              </Link>
              <Link
                href="/reports"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("reports")}
              </Link>
              <Link
                href="/inventory"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("inventory")}
              </Link>
              <Link
                href="/suppliers"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("suppliers")}
              </Link>
              <Link
                href="/logistics"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("logistics")}
              </Link>
              <Link
                href="/customers"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("customers")}
              </Link>
              <Link
                href="/invoices"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("invoices")}
              </Link>
              <Link
                href="/edge-agents"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("edgeAgents")}
              </Link>
              {isOwner && (
                <Link
                  href="/settings/accounts"
                  data-permission-public="true"
                  className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
                >
                  {t("accounts")}
                </Link>
              )}
              <Link
                href="/settings/members"
                data-permission-public="true"
                className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
              >
                {t("members")}
              </Link>
              {isOwner && (
                <Link
                  href="/audit-log"
                  data-permission-public="true"
                  className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
                >
                  {t("auditLog")}
                </Link>
              )}
              {isOwner && (
                <Link
                  href="/settings/permissions"
                  data-permission-public="true"
                  className="text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
                >
                  {t("permissions")}
                </Link>
              )}
            </nav>
          )}
        </div>

        {/* Item 3's firm-wide search entry point - mounted here so it's
            reachable from every authenticated page, not just /search
            itself (see components/Nav/GlobalSearchBox.tsx). */}
        {activeFirmId && <GlobalSearchBox />}

        {/* eslint-disable-next-line @next/next/no-html-link-for-pages -- /api/auth/logout is a route handler, not a page: it must be a real navigation, not client-side routing */}
        <a
          href="/api/auth/logout"
          data-permission-public="true"
          className="text-sm font-medium text-zinc-700 underline-offset-4 hover:underline dark:text-zinc-300"
        >
          {t("logout")}
        </a>
      </header>

      <AuditModeClient firmId={activeFirmId} isOwner={isOwner} />
    </>
  );
}
