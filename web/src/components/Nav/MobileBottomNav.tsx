"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { usePathname } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import { IconHome, IconOperations, IconFinance, IconHr, IconSettings } from "./icons";

type BottomItem = {
  href: string;
  labelKey: string;
  icon: (props: { className?: string }) => React.ReactElement;
};

// The 5 destinations the batch brief calls "most important": Dashboard,
// Workflows, Invoices, Customers, Settings. /settings/members (not e.g.
// /settings/accounts) is the Settings destination deliberately - it's the
// one settings page a non-owner member can actually reach
// (NavLinks.tsx's GROUPS shows every other /settings/* link is
// `ownerOnly`), so this is the only choice that doesn't 403 for most
// users landing on it from the bottom bar.
const ITEMS: BottomItem[] = [
  { href: "/", labelKey: "dashboard", icon: IconHome },
  { href: "/workflows", labelKey: "workflows", icon: IconOperations },
  { href: "/invoices", labelKey: "invoices", icon: IconFinance },
  { href: "/customers", labelKey: "customers", icon: IconHr },
  { href: "/settings/members", labelKey: "settings", icon: IconSettings },
];

// New this batch: since a 48px icon-only sidebar rail (NavShell.tsx) is
// hard to use reliably on a real mobile touch target, this fixed bottom
// bar sits ALONGSIDE it on mobile (not instead of it) - both together is
// the brief's explicit intent, giving mobile a second, larger-target way
// to reach the handful of most-used destinations. Hidden at `lg`+, the
// same breakpoint where the sidebar itself widens to the full labeled
// layout and a bottom bar would be redundant.
export default function MobileBottomNav() {
  const t = useTranslations("Nav");
  const pathname = usePathname();

  function isActive(href: string): boolean {
    if (href === "/") return pathname === "/";
    return pathname === href || pathname.startsWith(`${href}/`);
  }

  return (
    <nav
      aria-label={t("bottomNavLabel")}
      className="fixed inset-x-0 bottom-0 z-30 flex items-stretch justify-around border-t border-[var(--color-sidebar-border)] bg-[var(--color-sidebar-bg)] pb-[env(safe-area-inset-bottom)] lg:hidden"
    >
      {ITEMS.map((item) => {
        const active = isActive(item.href);
        const Icon = item.icon;
        return (
          <Link
            key={item.href}
            href={item.href}
            data-permission-public="true"
            aria-current={active ? "page" : undefined}
            className="flex flex-1 flex-col items-center justify-center gap-0.5 py-2 text-[10px]"
          >
            <span
              className={`h-1 w-1 rounded-full transition-colors ${
                active ? "bg-[var(--color-sidebar-fg)]" : "bg-transparent"
              }`}
              aria-hidden="true"
            />
            <Icon
              className={`h-4 w-4 ${
                active ? "text-[var(--color-sidebar-fg)]" : "text-[var(--color-sidebar-fg-muted)]"
              }`}
            />
            <span className={active ? "text-[var(--color-sidebar-fg)]" : "text-[var(--color-sidebar-fg-muted)]"}>
              {t(item.labelKey)}
            </span>
          </Link>
        );
      })}
    </nav>
  );
}
