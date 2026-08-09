// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { markNotificationRead } from "@/lib/notifications";

type RouteParams = {
  params: Promise<{ firmId: string; id: string }>;
};

// Proxies the Go backend's PATCH .../notifications/{id}/read.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, id } = await params;
  const ok = await markNotificationRead(token, firmId, id);
  if (!ok) {
    return NextResponse.json({ error: "failed to mark notification read" }, { status: 502 });
  }
  return new NextResponse(null, { status: 204 });
}
