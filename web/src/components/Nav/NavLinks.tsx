"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { usePathname } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";

type NavItem = {
  href: string;
  labelKey: string;
  ownerOnly?: boolean;
};

type NavGroup = {
  key: string;
  labelKey: string;
  icon: string;
  items: NavItem[];
};

// Every link the old flat header (NavShell.tsx, pre-sidebar) carried,
// grouped into the five sections this batch's brief calls for. No link
// was dropped or renamed - this is a pure regrouping. `icon` is a
// single-letter abbreviation (no new icon library dependency, per the
// batch brief's explicit "keep it simple" fallback) shown in the
// icon-only collapsed state below the `lg` breakpoint.
const GROUPS: NavGroup[] = [
  {
    key: "operations",
    labelKey: "groupOperations",
    icon: "O",
    items: [
      { href: "/stock", labelKey: "stock" },
      { href: "/workflows", labelKey: "workflows" },
      { href: "/inventory", labelKey: "inventory" },
      { href: "/suppliers", labelKey: "suppliers" },
      { href: "/logistics", labelKey: "logistics" },
      { href: "/customers", labelKey: "customers" },
      { href: "/tasks", labelKey: "tasks" },
      { href: "/approvals", labelKey: "approvals" },
      { href: "/edge-agents", labelKey: "edgeAgents" },
    ],
  },
  {
    key: "finance",
    labelKey: "groupFinance",
    icon: "F",
    items: [
      { href: "/financials", labelKey: "financials" },
      { href: "/invoices", labelKey: "invoices" },
      { href: "/reports", labelKey: "reports" },
      { href: "/hr/payroll", labelKey: "payroll" },
    ],
  },
  {
    key: "hr",
    labelKey: "groupHr",
    icon: "H",
    items: [
      { href: "/hr", labelKey: "hr" },
      { href: "/hr/time", labelKey: "timeTracking" },
      { href: "/hr/absences", labelKey: "absences" },
    ],
  },
  {
    key: "settings",
    labelKey: "groupSettings",
    icon: "S",
    items: [
      { href: "/settings/accounts", labelKey: "accounts", ownerOnly: true },
      { href: "/settings/addresses", labelKey: "addresses", ownerOnly: true },
      { href: "/settings/tax-rates", labelKey: "taxRates", ownerOnly: true },
      { href: "/settings/document-templates", labelKey: "documentTemplates", ownerOnly: true },
      { href: "/settings/api-keys", labelKey: "apiKeys", ownerOnly: true },
      { href: "/settings/webhooks", labelKey: "webhooks", ownerOnly: true },
      { href: "/settings/members", labelKey: "members" },
    ],
  },
  {
    key: "administration",
    labelKey: "groupAdministration",
    icon: "A",
    items: [
      { href: "/audit-log", labelKey: "auditLog", ownerOnly: true },
      { href: "/settings/permissions", labelKey: "permissions", ownerOnly: true },
    ],
  },
];

type Props = {
  isOwner: boolean;
};

// The sidebar's interactive link list - split off from the (Server
// Component) NavShell purely because active-route highlighting needs
// usePathname(), same pattern this codebase already uses for
// AuditModeClient/FirmSwitcher/NotificationBell (small client islands
// composed into a server-rendered shell, not the whole shell turned
// client). Every href/labelKey/ownerOnly gate below is unchanged from
// the prior flat header - see GROUPS' own doc comment.
export default function NavLinks({ isOwner }: Props) {
  const t = useTranslations("Nav");
  const pathname = usePathname();

  // pathname from next-intl's Link-aware usePathname is already
  // locale-stripped (e.g. "/stock", not "/en/stock").
  function isActive(href: string): boolean {
    if (href === "/") return pathname === "/";
    return pathname === href || pathname.startsWith(`${href}/`);
  }

  return (
    <nav className="flex flex-col gap-5 overflow-y-auto px-2 py-4" aria-label={t("brand")}>
      {GROUPS.map((group) => {
        const items = group.items.filter((item) => !item.ownerOnly || isOwner);
        if (items.length === 0) return null;
        return (
          <div key={group.key} className="flex flex-col gap-1">
            <span
              className="hidden px-3 text-xs font-medium tracking-wide text-zinc-500 uppercase lg:block dark:text-zinc-500"
              aria-hidden="true"
            >
              {t(group.labelKey)}
            </span>
            {items.map((item) => {
              const active = isActive(item.href);
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  data-permission-public="true"
                  aria-current={active ? "page" : undefined}
                  title={t(item.labelKey)}
                  className={`flex items-center gap-3 rounded-md px-3 py-1.5 text-sm transition-colors ${
                    active
                      ? "bg-zinc-100 font-medium text-black dark:bg-zinc-900 dark:text-zinc-50"
                      : "text-zinc-700 hover:bg-zinc-50 dark:text-zinc-300 dark:hover:bg-zinc-950"
                  }`}
                >
                  <span
                    className="flex h-5 w-5 shrink-0 items-center justify-center rounded border border-zinc-300 text-[10px] font-semibold text-zinc-500 lg:hidden dark:border-zinc-700 dark:text-zinc-400"
                    aria-hidden="true"
                  >
                    {group.icon}
                  </span>
                  <span className="hidden lg:inline">{t(item.labelKey)}</span>
                </Link>
              );
            })}
          </div>
        );
      })}
    </nav>
  );
}
