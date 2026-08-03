// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

"use client";

import { useEffect } from "react";
import { usePathname } from "next/navigation";
import { init, recordPageView } from "@/lib/telemetry";

// Mounted once near the root layout (see app/[locale]/layout.tsx),
// renders nothing - starts lib/telemetry.ts's periodic flush loop and
// records one page-view count per route change. Both init() and
// recordPageView() are true no-ops when
// NEXT_PUBLIC_ZONARYOS_TELEMETRY_ENABLED isn't exactly "true" - see
// lib/telemetry.ts's doc comment for the full default-off guarantee;
// this component itself always renders (so its absence is never a
// visible layout difference), it's the library calls underneath that
// stay inert.
export default function TelemetryClient() {
  const pathname = usePathname();

  useEffect(() => {
    init();
  }, []);

  useEffect(() => {
    if (pathname) recordPageView(pathname);
  }, [pathname]);

  return null;
}
