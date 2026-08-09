// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchUnreadCount } from "@/lib/notifications";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET .../notifications/unread-count - the
// lightweight nav-badge query NotificationBell polls.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const count = await fetchUnreadCount(token, firmId);
  if (count === null) {
    return NextResponse.json({ error: "failed to load unread count" }, { status: 502 });
  }
  return NextResponse.json({ count });
}
