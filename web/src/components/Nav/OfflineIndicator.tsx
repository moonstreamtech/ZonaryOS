// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).
"use client";

import { useSyncExternalStore } from "react";
import { useTranslations } from "next-intl";

// PWA foundation batch, Part 2: a small connectivity indicator, mounted
// alongside NotificationBell in NavShell's sidebar bottom section (per
// the batch brief, "the bell area"). Renders nothing while online; a
// small amber dot with an accessible label when offline. Reacts to the
// browser's `online`/`offline` events (no page reload needed) via
// `useSyncExternalStore` - the correct hook for "subscribe to state that
// lives outside React" (here, the browser's own connectivity state)
// rather than a useState+useEffect pair that would call setState
// synchronously inside the effect body.
function subscribe(callback: () => void) {
  window.addEventListener("online", callback);
  window.addEventListener("offline", callback);
  return () => {
    window.removeEventListener("online", callback);
    window.removeEventListener("offline", callback);
  };
}

function getSnapshot() {
  return !navigator.onLine;
}

// navigator.onLine isn't available during SSR - assume online (matches
// the common case, and avoids the offline indicator flashing on every
// server-rendered page load) until the client snapshot takes over.
function getServerSnapshot() {
  return false;
}

export default function OfflineIndicator() {
  const t = useTranslations("Offline");
  const isOffline = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);

  if (!isOffline) return null;

  return (
    <span
      role="status"
      aria-label={t("offlineLabel")}
      title={t("offlineLabel")}
      className="flex items-center justify-center gap-1.5 rounded-md px-1.5 py-1 text-[10px] font-medium text-amber-400"
    >
      <span aria-hidden="true" className="h-2 w-2 shrink-0 rounded-full bg-amber-400" />
      <span className="hidden lg:inline">{t("offlineIndicator")}</span>
    </span>
  );
}
