"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { usePathname } from "next/navigation";
import { useTranslations } from "next-intl";
import { Link } from "@/i18n/navigation";
import {
  IconOperations,
  IconFinance,
  IconHr,
  IconSettings,
  IconAdministration,
} from "./icons";

type NavItem = {
  href: string;
  labelKey: string;
  ownerOnly?: boolean;
};

type IconComponent = (props: { className?: string }) => React.ReactElement;

type NavGroup = {
  key: string;
  labelKey: string;
  icon: IconComponent;
  items: NavItem[];
};

// Every link the old flat header (NavShell.tsx, pre-sidebar) carried,
// grouped into the five sections this batch's brief calls for. No link
// was dropped or renamed - this is a pure regrouping. `icon` is a real
// SVG glyph shared by every item in the group (see icons.tsx's doc
// comment for why per-GROUP icons, not 20+ bespoke per-item icons) shown
// both in the icon-only rail below `lg` and next to the label at `lg`+.
const GROUPS: NavGroup[] = [
  {
    key: "operations",
    labelKey: "groupOperations",
    icon: IconOperations,
    items: [
      { href: "/stock", labelKey: "stock" },
      { href: "/workflows", labelKey: "workflows" },
      { href: "/inventory", labelKey: "inventory" },
      { href: "/suppliers", labelKey: "suppliers" },
      { href: "/logistics", labelKey: "logistics" },
      { href: "/sales-orders", labelKey: "salesOrders" },
      { href: "/customers", labelKey: "customers" },
      { href: "/tasks", labelKey: "tasks" },
      { href: "/approvals", labelKey: "approvals" },
      { href: "/edge-agents", labelKey: "edgeAgents" },
    ],
  },
  {
    key: "finance",
    labelKey: "groupFinance",
    icon: IconFinance,
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
    icon: IconHr,
    items: [
      { href: "/hr", labelKey: "hr" },
      { href: "/hr/time", labelKey: "timeTracking" },
      { href: "/hr/absences", labelKey: "absences" },
    ],
  },
  {
    key: "settings",
    labelKey: "groupSettings",
    icon: IconSettings,
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
    icon: IconAdministration,
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
//
// Dark-sidebar rework (this batch): colors are fixed (not `dark:`
// conditional) because the sidebar is always a dark surface regardless
// of OS theme - see tokens.css's --color-sidebar-* doc comment. Layout
// (icon-only rail vs. full label list) still switches on the `lg`
// breakpoint via pure CSS, same mechanism as before, just retuned widths
// (see NavShell.tsx).
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
        const GroupIcon = group.icon;
        return (
          <div key={group.key} className="flex flex-col gap-1">
            <span
              className="hidden px-3 text-xs font-medium tracking-wide text-[var(--color-sidebar-fg-muted)] uppercase lg:block"
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
                  className={`group flex items-center justify-center gap-3 rounded-md border-l-2 py-2 text-sm transition-colors lg:justify-start lg:px-3 lg:py-1.5 ${
                    active
                      ? "border-[var(--color-sidebar-fg)] bg-[var(--color-sidebar-active)] font-medium text-[var(--color-sidebar-fg)]"
                      : "border-transparent text-[var(--color-sidebar-fg-muted)] hover:bg-[var(--color-sidebar-hover)] hover:text-[var(--color-sidebar-fg)]"
                  }`}
                >
                  <GroupIcon className="h-4 w-4" />
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
