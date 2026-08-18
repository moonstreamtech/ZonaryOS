// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchSyncLogs } from "@/lib/integrations";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET
// /api/firms/{firmId}/integration-sync-logs (member-only, read-only),
// optionally filtered to one connector via ?connectorId= - fetched
// client-side, same reasoning as the mapping list's own sibling route.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const connectorId = request.nextUrl.searchParams.get("connectorId") ?? undefined;
  const logs = await fetchSyncLogs(token, firmId, connectorId);
  if (logs === null) {
    return NextResponse.json({ error: "failed to load sync logs" }, { status: 400 });
  }
  return NextResponse.json(logs);
}
