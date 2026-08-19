"use client";
// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { useEffect } from "react";
import { usePathname } from "next/navigation";

type Props = {
  firmId: string | null;
};

// Mounted once near the root layout (see app/[locale]/layout.tsx),
// renders nothing - fires a page_view analytics event on every route
// change, same "side-effect-only client component mounted once" shape as
// TelemetryClient. Fire-and-forget, never blocking: prefers
// navigator.sendBeacon (survives the page unloading mid-request, exactly
// the case a route-change beacon needs to tolerate) and falls back to a
// plain non-awaited fetch(...).catch(() => {}) when sendBeacon isn't
// available (older browsers, or the currently-unavailable-in-tests jsdom
// environment) - either way this never throws into the render, and
// there's nothing here for a caller to await.
export default function PageViewTracker({ firmId }: Props) {
  const pathname = usePathname();

  useEffect(() => {
    if (!firmId || !pathname) return;

    const body = JSON.stringify([{ eventType: "page_view", properties: { page: pathname } }]);
    const url = `/api/analytics/events/${firmId}`;

    try {
      if (typeof navigator !== "undefined" && typeof navigator.sendBeacon === "function") {
        const blob = new Blob([body], { type: "application/json" });
        const sent = navigator.sendBeacon(url, blob);
        if (sent) return;
      }
    } catch {
      // Falls through to fetch below.
    }

    fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
      keepalive: true,
    }).catch(() => {
      // Never surfaces - a dropped analytics beacon must not disrupt navigation.
    });
  }, [firmId, pathname]);

  return null;
}
