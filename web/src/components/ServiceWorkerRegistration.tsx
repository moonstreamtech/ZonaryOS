// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).
"use client";

import { useEffect } from "react";

// PWA foundation batch, Part 2: registers public/sw.js on mount. Renders
// nothing - purely a side effect, same "invisible client component
// mounted once from the root layout" shape as Telemetry/TelemetryClient
// .tsx. Guarded by the standard feature-detect (older browsers, and any
// browser with service workers disabled, simply never register one -
// the app still works, just without the offline/PWA behavior).
export default function ServiceWorkerRegistration() {
  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;
    navigator.serviceWorker.register("/sw.js").catch(() => {
      // best-effort - the app still works fully online without a
      // registered service worker, so a registration failure here isn't
      // surfaced to the user.
    });
  }, []);

  return null;
}
