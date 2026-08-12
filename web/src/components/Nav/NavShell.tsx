// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations } from "next-intl/server";
import { Link } from "@/i18n/navigation";
import type { MeResponse } from "@/lib/me";
import FirmSwitcher from "./FirmSwitcher";
import NavLinks from "./NavLinks";
import AuditModeClient from "@/components/AuditMode/AuditModeClient";

type Props = {
  me: MeResponse | null;
  activeFirmId: string | null;
  isOwner: boolean;
};

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
  return (parts[0]![0]! + parts[parts.length - 1]![0]!).toUpperCase();
}

// Persistent left sidebar nav (restructured this batch from the prior
// flat top `<header>` - see NavLinks.tsx for the grouped link list,
// unchanged from the old header link-for-link). Mounted once in the root
// layout so it's present on every authenticated page (item 5). Renders
// nothing for a signed-out visitor - the homepage still owns its own
// sign-in call to action.
//
// The Audit Mode toggle (components/AuditMode/AuditModeClient.tsx) is
// rendered from here rather than directly in the root layout, same
// reasoning as before this restructure: this shell is the one place that
// owns "chrome shown on every authenticated page."
//
// GlobalSearchBox and NotificationBell moved out of this component with
// the sidebar restructure - they're mounted in the root layout's slim
// top bar now (see app/[locale]/layout.tsx), since a search box and a
// notification bell don't fit a narrow vertical rail the way a list of
// section links does.
export default async function NavShell({ me, activeFirmId, isOwner }: Props) {
  const t = await getTranslations("Nav");

  if (!me) {
    return <AuditModeClient firmId={activeFirmId} isOwner={isOwner} />;
  }

  const displayName = me.displayName || me.email;

  return (
    <>
      <aside className="flex w-14 shrink-0 flex-col border-r border-zinc-200 bg-white lg:w-60 dark:border-zinc-800 dark:bg-black">
        <Link
          href="/"
          data-permission-public="true"
          className="flex items-center gap-2 border-b border-zinc-200 px-3 py-4 text-sm font-semibold tracking-tight text-black dark:border-zinc-800 dark:text-zinc-50"
        >
          <span
            className="flex h-6 w-6 shrink-0 items-center justify-center rounded bg-black text-xs font-bold text-white dark:bg-zinc-50 dark:text-black"
            aria-hidden="true"
          >
            Z
          </span>
          <span className="hidden truncate lg:inline">{t("brand")}</span>
        </Link>

        {activeFirmId && (
          <>
            <div className="flex-1 overflow-y-auto">
              <NavLinks isOwner={isOwner} />
            </div>

            <div className="flex flex-col gap-2 border-t border-zinc-200 px-2 py-3 dark:border-zinc-800">
              <div className="px-1">
                <FirmSwitcher firms={me.firms} activeFirmId={activeFirmId} />
              </div>

              <div className="flex items-center gap-2 px-1">
                <span
                  className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-zinc-200 text-xs font-semibold text-zinc-700 dark:bg-zinc-800 dark:text-zinc-200"
                  aria-hidden="true"
                  title={displayName}
                >
                  {initials(displayName)}
                </span>
                <span className="hidden truncate text-xs text-zinc-600 lg:inline dark:text-zinc-400">
                  {displayName}
                </span>
              </div>

              {/* eslint-disable-next-line @next/next/no-html-link-for-pages -- /api/auth/logout is a route handler, not a page: it must be a real navigation, not client-side routing */}
              <a
                href="/api/auth/logout"
                data-permission-public="true"
                className="rounded-md px-3 py-1.5 text-sm font-medium text-zinc-700 hover:bg-zinc-50 dark:text-zinc-300 dark:hover:bg-zinc-950"
              >
                <span className="lg:hidden" aria-hidden="true">
                  ⏻
                </span>
                <span className="hidden lg:inline">{t("logout")}</span>
              </a>
            </div>
          </>
        )}
      </aside>

      <AuditModeClient firmId={activeFirmId} isOwner={isOwner} />
    </>
  );
}
