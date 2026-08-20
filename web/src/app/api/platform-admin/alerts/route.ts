// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchAlerts } from "@/lib/alerts";

// Proxies the Go backend's GET /api/platform-admin/alerts
// (internal/platformadmin.Allowlist-gated) - no authorization decision
// made here, same convention as every other platform-admin proxy route.
// Also used by the platform-admin nav's alert badge (a lightweight
// fetch-on-mount plus interval poll, see Nav/PlatformAdminAlertBadge.tsx),
// not just the /platform-admin/alerts page itself.
export async function GET(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const alerts = await fetchAlerts(token);
  if (alerts === null) {
    return NextResponse.json({ error: "failed to load alerts" }, { status: 404 });
  }
  return NextResponse.json(alerts);
}
