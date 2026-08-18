"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useEffect, useRef, useState } from "react";
import { useTranslations } from "next-intl";
import { useRouter } from "@/i18n/navigation";
import type { Notification } from "@/lib/notifications";

type Props = {
  firmId: string;
};

// The unread-count nav badge (Never-Violate scope note: no email/push,
// no WebSocket - polling is an accepted, documented simplification for a
// single-instance deployment, see internal/notification's own doc
// comment). Polls GET .../notifications/unread-count every 30s; clicking
// the bell opens a small dropdown that fetches the most recent
// notifications on demand rather than keeping them polled too. Clicking
// a notification marks it read and navigates: an approval-related
// notification (payload.pendingApprovalId present) goes to /approvals,
// where the actual accept/reject action lives - there is no dedicated
// per-instance page yet for this to link to instead (see
// docs/DEVELOPMENT.md's Notification/approval section for why).
export default function NotificationBell({ firmId }: Props) {
  const t = useTranslations("Notifications");
  const router = useRouter();
  const [unreadCount, setUnreadCount] = useState(0);
  const [open, setOpen] = useState(false);
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [loading, setLoading] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let cancelled = false;

    async function refreshUnreadCount() {
      try {
        const res = await fetch(`/api/notifications/${firmId}/unread-count`, { cache: "no-store" });
        if (!res.ok || cancelled) return;
        const data = (await res.json()) as { count: number };
        if (!cancelled) setUnreadCount(data.count);
      } catch {
        // best-effort - the badge simply won't update this cycle.
      }
    }

    refreshUnreadCount();
    const interval = setInterval(refreshUnreadCount, 30_000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [firmId]);

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  async function toggleOpen() {
    const next = !open;
    setOpen(next);
    if (next) {
      setLoading(true);
      try {
        const res = await fetch(`/api/notifications/${firmId}?limit=10`, { cache: "no-store" });
        if (res.ok) {
          setNotifications((await res.json()) as Notification[]);
        }
      } finally {
        setLoading(false);
      }
    }
  }

  async function handleSelect(n: Notification) {
    setOpen(false);
    if (!n.readAt) {
      try {
        await fetch(`/api/notifications/${firmId}/${n.id}/read`, { method: "PATCH" });
        setUnreadCount((c) => Math.max(0, c - 1));
      } catch {
        // best-effort - the notification stays unread, retryable next time.
      }
    }
    if (typeof n.payload.pendingApprovalId === "string") {
      router.push("/approvals");
    }
  }

  return (
    <div ref={containerRef} className="relative">
      <button
        type="button"
        data-permission-public="true"
        onClick={toggleOpen}
        aria-label={t("bellLabel")}
        className="relative rounded-md p-1.5 text-[var(--color-sidebar-fg-muted)] hover:bg-[var(--color-sidebar-hover)] hover:text-[var(--color-sidebar-fg)]"
      >
        <span aria-hidden="true">🔔</span>
        {unreadCount > 0 && (
          <span className="absolute -right-0.5 -top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-red-600 px-1 text-[10px] font-medium text-white">
            {unreadCount > 99 ? "99+" : unreadCount}
          </span>
        )}
      </button>

      {open && (
        <div className="absolute right-0 z-20 mt-2 w-80 rounded-md border border-zinc-300 bg-white shadow-lg dark:border-zinc-700 dark:bg-black">
          <div className="border-b border-zinc-200 px-3 py-2 text-sm font-semibold text-black dark:border-zinc-800 dark:text-zinc-50">
            {t("title")}
          </div>
          <div className="max-h-96 overflow-y-auto">
            {loading ? (
              <p className="px-3 py-4 text-sm text-zinc-600 dark:text-zinc-400">{t("loading")}</p>
            ) : notifications.length === 0 ? (
              <p className="px-3 py-4 text-sm text-zinc-600 dark:text-zinc-400">{t("empty")}</p>
            ) : (
              <ul>
                {notifications.map((n) => (
                  <li key={n.id}>
                    <button
                      type="button"
                      data-permission-public="true"
                      onClick={() => handleSelect(n)}
                      className={`flex w-full flex-col gap-0.5 border-b border-zinc-100 px-3 py-2 text-start text-sm hover:bg-zinc-50 dark:border-zinc-900 dark:hover:bg-zinc-900 ${
                        n.readAt ? "opacity-60" : ""
                      }`}
                    >
                      <span className="font-medium text-black dark:text-zinc-50">{n.title}</span>
                      <span className="text-xs text-zinc-600 dark:text-zinc-400">{n.body}</span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
