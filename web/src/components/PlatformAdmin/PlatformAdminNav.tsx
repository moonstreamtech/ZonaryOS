"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useEffect, useState } from "react";
import { useTranslations } from "next-intl";

type Props = {
  locale: string;
  active: "home" | "firmGroups" | "exchangeRates" | "metrics" | "alerts";
};

// Small in-section nav bar for the platform-admin area (firms/firm-groups/
// exchange-rates/metrics/alerts) - there is deliberately no link to any of
// this from the app's own NavShell (see platform-admin/page.tsx's own doc
// comment: this whole area is reachable only by direct URL navigation for
// the small allowlisted staff set, never surfaced to ordinary firm users).
// This bar exists purely to cross-link the platform-admin pages to each
// other once a staff member has already landed on one of them, not to
// advertise the area more broadly.
//
// The "Alerts" link carries a small unread-style badge - this batch's
// alert events never auto-resolve, so "count of alert events from the
// last 24h" is what's shown, following the same fetch-on-mount-plus-
// interval-poll shape as Nav/NotificationBell.tsx (30s interval, silently
// giving up on a failed fetch rather than surfacing an error here).
export default function PlatformAdminNav({ locale, active }: Props) {
  const t = useTranslations("PlatformAdmin");
  const [alertCount, setAlertCount] = useState<number | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function refreshAlertCount() {
      try {
        const res = await fetch(`/api/platform-admin/alerts`, { cache: "no-store" });
        if (!res.ok || cancelled) return;
        const data = (await res.json()) as { triggeredAt: string }[];
        if (cancelled) return;
        const dayAgo = Date.now() - 24 * 60 * 60 * 1000;
        const recent = data.filter((a) => new Date(a.triggeredAt).getTime() >= dayAgo);
        setAlertCount(recent.length);
      } catch {
        // best-effort - the badge simply won't update this cycle.
      }
    }

    refreshAlertCount();
    const interval = setInterval(refreshAlertCount, 30_000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, []);

  const links: { key: Props["active"]; href: string; label: string }[] = [
    { key: "home", href: `/${locale}/platform-admin`, label: t("navHome") },
    { key: "firmGroups", href: `/${locale}/platform-admin/firm-groups`, label: t("navFirmGroups") },
    { key: "exchangeRates", href: `/${locale}/platform-admin/exchange-rates`, label: t("navExchangeRates") },
    { key: "metrics", href: `/${locale}/platform-admin/metrics`, label: t("navMetrics") },
    { key: "alerts", href: `/${locale}/platform-admin/alerts`, label: t("navAlerts") },
  ];

  return (
    <nav className="flex w-full max-w-4xl flex-wrap items-center gap-2">
      {links.map((link) => (
        <a
          key={link.key}
          href={link.href}
          data-permission-public="true"
          className={`relative rounded-md px-3 py-1.5 text-sm font-medium ${
            active === link.key
              ? "bg-[var(--color-platform-accent-strong)] text-[var(--color-platform-on-accent)]"
              : "text-indigo-200 hover:bg-black/20"
          }`}
        >
          {link.label}
          {link.key === "alerts" && alertCount !== null && alertCount > 0 && (
            <span className="absolute -right-1.5 -top-1.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-red-600 px-1 text-[10px] font-medium text-white">
              {alertCount > 99 ? "99+" : alertCount}
            </span>
          )}
        </a>
      ))}
    </nav>
  );
}
