// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).
"use client";

import { useEffect, useState, useCallback } from "react";
import { useTranslations } from "next-intl";
import { listQueue, QUEUE_CHANGED_EVENT } from "@/lib/offlineQueue";

// Offline support batch, Part 3: a nav-area indicator of the offline
// queue's contents (lib/offlineQueue.ts), mounted alongside
// NotificationBell/OfflineIndicator in NavShell's sidebar bottom section
// ("the bell area"). Reads the real IndexedDB-backed queue via
// `listQueue` - refreshed on the `zonaryos:offline-queue-changed`
// CustomEvent offlineQueue.ts dispatches on every enqueue/replay step,
// plus a slow interval poll as a fallback for any change that doesn't
// go through that event (belt-and-braces, cheap since `listQueue` is a
// single IndexedDB read). Purely informational - no retry button here,
// since replay already re-attempts a failed operation automatically the
// next time it runs (see replayQueue's own doc comment); nothing here is
// an interactive/permission-gated control.
const POLL_INTERVAL_MS = 15_000;

export default function SyncStatusIndicator() {
  const t = useTranslations("Offline");
  const [pendingCount, setPendingCount] = useState(0);
  const [failedCount, setFailedCount] = useState(0);

  const refresh = useCallback(() => {
    listQueue()
      .then((queue) => {
        setPendingCount(queue.filter((op) => op.status === "pending" || op.status === "syncing").length);
        setFailedCount(queue.filter((op) => op.status === "failed").length);
      })
      .catch(() => {
        // best-effort - IndexedDB unavailable (e.g. private browsing in
        // some browsers) simply means no queue counts to show.
      });
  }, []);

  useEffect(() => {
    refresh();
    window.addEventListener(QUEUE_CHANGED_EVENT, refresh);
    const interval = setInterval(refresh, POLL_INTERVAL_MS);
    return () => {
      window.removeEventListener(QUEUE_CHANGED_EVENT, refresh);
      clearInterval(interval);
    };
  }, [refresh]);

  if (pendingCount === 0 && failedCount === 0) return null;

  const label =
    failedCount > 0
      ? t("syncFailed", { count: failedCount })
      : t("syncPending", { count: pendingCount });

  return (
    <span
      role="status"
      aria-label={t("syncStatusLabel")}
      title={label}
      className="flex items-center justify-center gap-1.5 rounded-md px-1.5 py-1 text-[10px] font-medium"
    >
      <span
        aria-hidden="true"
        className={`h-2 w-2 shrink-0 rounded-full ${failedCount > 0 ? "bg-red-500" : "bg-sky-400"}`}
      />
      <span className={`hidden lg:inline ${failedCount > 0 ? "text-red-400" : "text-sky-400"}`}>{label}</span>
    </span>
  );
}
