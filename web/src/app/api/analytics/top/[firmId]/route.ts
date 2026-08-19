// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchTopByProperty } from "@/lib/analytics";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET /api/firms/{firmId}/analytics/top -
// shared behind both "top workflows by execution count" and the
// page-view heatmap table, same as the endpoint it wraps (see
// internal/analytics/handlers.go's own handleTopByProperty doc comment).
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const eventType = request.nextUrl.searchParams.get("eventType") ?? "";
  const property = request.nextUrl.searchParams.get("property") ?? "";
  const limitParam = request.nextUrl.searchParams.get("limit");
  const limit = limitParam ? Number(limitParam) : 20;

  const counts = await fetchTopByProperty(token, firmId, eventType, property, limit);
  if (counts === null) {
    return NextResponse.json({ error: "failed to load top-by-property" }, { status: 502 });
  }
  return NextResponse.json(counts);
}
