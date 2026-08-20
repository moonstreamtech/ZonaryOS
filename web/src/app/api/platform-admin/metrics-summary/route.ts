// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchMetricsSummary } from "@/lib/perfMetrics";

// Proxies the Go backend's GET /api/platform-admin/metrics/summary
// (internal/platformadmin.Allowlist-gated) - no authorization decision
// made here, same convention as every other platform-admin proxy route
// (see api/platform-admin/exchange-rates/route.ts). Read-only, so this
// exists purely so the client-side metrics dashboard doesn't need its own
// bearer token - it calls this route with the session cookie instead,
// same pattern api/platform-admin/firm-groups/route.ts's POST handler
// uses for the write side.
export async function GET(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const summary = await fetchMetricsSummary(token);
  if (summary === null) {
    return NextResponse.json({ error: "failed to load metrics summary" }, { status: 404 });
  }
  return NextResponse.json(summary);
}
