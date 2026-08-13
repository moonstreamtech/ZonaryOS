// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { getTranslations } from "next-intl/server";
import { Link } from "@/i18n/navigation";
import type { MeResponse } from "@/lib/me";
import FirmSwitcher from "./FirmSwitcher";
import NavLinks from "./NavLinks";
import GlobalSearchBox from "./GlobalSearchBox";
import NotificationBell from "./NotificationBell";
import MobileBottomNav from "./MobileBottomNav";
import { IconSearch, IconLogout } from "./icons";
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

// Persistent left sidebar nav, always visible (mobile-nav rework batch:
// previously `w-14 lg:w-60` looked collapsed-to-nothing on real mobile
// Chrome below the `lg` breakpoint - 56px with no room to read anything.
// Now a real, deliberately-sized 48px (`w-12`) icon-only rail at every
// width up to `lg`, widening to the full 240px (`w-60`) labeled sidebar
// at `lg`+ - same breakpoint the rest of this component tree (and this
// same batch's earlier one) already uses for its own label/full-content
// switches, kept for consistency rather than introducing a second
// `md`-based cutover. See NavLinks.tsx for the grouped link list itself.
//
// Dark surface throughout (zinc-900 / #18181b per the batch's design
// brief, defined once as tokens in styles/tokens.css rather than
// hardcoded here) - deliberately NOT toggled by OS light/dark theme like
// the rest of the app's content area: the sidebar is meant to read like
// the app's own Keycloak login screen's dark, geometric theme regardless
// of system theme, while the content area stays on its existing light
// zinc-50/white system.
//
// GlobalSearchBox and NotificationBell now live in this sidebar's bottom
// section (search as a full input at `lg`+, collapsing to an icon-only
// trigger on the rail; the bell is icon-only at every width already) -
// moved out of the root layout's old separate top bar so mobile page
// content starts at the very top with nothing above it (see
// app/[locale]/layout.tsx). There's no separate `lg`+ top bar either:
// one placement for every width keeps the shell simpler and avoids
// duplicating the same widget.
//
// MobileBottomNav (new this batch) is mounted from here too, alongside
// the rail rather than instead of it - see that component's own doc
// comment for why both together, not one replacing the other.
//
// The Audit Mode toggle (components/AuditMode/AuditModeClient.tsx) is
// rendered from here rather than directly in the root layout, same
// reasoning as before this restructure: this shell is the one place that
// owns "chrome shown on every authenticated page."
export default async function NavShell({ me, activeFirmId, isOwner }: Props) {
  const t = await getTranslations("Nav");

  if (!me) {
    return <AuditModeClient firmId={activeFirmId} isOwner={isOwner} />;
  }

  const displayName = me.displayName || me.email;

  return (
    <>
      <aside className="flex w-12 shrink-0 flex-col bg-[var(--color-sidebar-bg)] lg:w-60">
        <Link
          href="/"
          data-permission-public="true"
          className="flex items-center justify-center gap-2 border-b border-[var(--color-sidebar-border)] px-2 py-4 text-sm font-semibold tracking-tight text-[var(--color-sidebar-fg)] lg:justify-start lg:px-4"
        >
          <span
            className="flex h-6 w-6 shrink-0 items-center justify-center rounded bg-[var(--color-sidebar-fg)] text-xs font-bold text-[var(--color-sidebar-bg)]"
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

            <div className="flex flex-col gap-3 border-t border-[var(--color-sidebar-border)] px-2 py-3">
              {/* Search: an icon-only trigger on the rail (a text input
                  doesn't fit 48px), a full inline box at `lg`+. */}
              <Link
                href="/search"
                data-permission-public="true"
                aria-label={t("searchLabel")}
                title={t("searchLabel")}
                className="flex items-center justify-center rounded-md p-1.5 text-[var(--color-sidebar-fg-muted)] hover:bg-[var(--color-sidebar-hover)] hover:text-[var(--color-sidebar-fg)] lg:hidden"
              >
                <IconSearch className="h-4 w-4" />
              </Link>
              <div className="hidden lg:block">
                <GlobalSearchBox variant="sidebar" />
              </div>

              <div className="flex items-center justify-center lg:justify-start">
                <NotificationBell firmId={activeFirmId} />
              </div>

              <div className="hidden px-1 lg:block">
                <FirmSwitcher firms={me.firms} activeFirmId={activeFirmId} />
              </div>

              <div className="flex items-center justify-center gap-2 px-1 lg:justify-start">
                <span
                  className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-[var(--color-sidebar-hover)] text-xs font-semibold text-[var(--color-sidebar-fg)]"
                  aria-hidden="true"
                  title={displayName}
                >
                  {initials(displayName)}
                </span>
                <span className="hidden truncate text-xs text-[var(--color-sidebar-fg-muted)] lg:inline">
                  {displayName}
                </span>
              </div>

              {/* eslint-disable-next-line @next/next/no-html-link-for-pages -- /api/auth/logout is a route handler, not a page: it must be a real navigation, not client-side routing */}
              <a
                href="/api/auth/logout"
                data-permission-public="true"
                className="flex items-center justify-center gap-2 rounded-md px-3 py-1.5 text-sm font-medium text-[var(--color-sidebar-fg-muted)] hover:bg-[var(--color-sidebar-hover)] hover:text-[var(--color-sidebar-fg)] lg:justify-start"
              >
                <IconLogout className="h-4 w-4 lg:hidden" />
                <span className="hidden lg:inline">{t("logout")}</span>
              </a>
            </div>
          </>
        )}
      </aside>

      {activeFirmId && <MobileBottomNav />}

      <AuditModeClient firmId={activeFirmId} isOwner={isOwner} />
    </>
  );
}
