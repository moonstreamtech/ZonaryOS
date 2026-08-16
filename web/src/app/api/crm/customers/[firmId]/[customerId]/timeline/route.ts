// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchTimeline } from "@/lib/crm";

type RouteParams = {
  params: Promise<{ firmId: string; customerId: string }>;
};

// Proxies the Go backend's GET
// /api/firms/{firmId}/customers/{customerId}/timeline - the merged,
// sorted interactions+opportunities feed. No authorization decision made
// here, same convention as the sibling interactions route.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, customerId } = await params;
  const timeline = await fetchTimeline(token, firmId, customerId);
  if (timeline === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(timeline);
}
