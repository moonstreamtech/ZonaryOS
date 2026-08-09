// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchNotifications } from "@/lib/notifications";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET /api/firms/{firmId}/notifications - member-
// gated, each caller sees only their own (internal/notification.ListForUser's
// own doc comment). Client components (NotificationBell) call this rather
// than fetching the Go backend directly, same convention as every other
// browser-facing API route in this codebase (the session cookie lives
// here, not the raw bearer token).
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const unreadOnly = request.nextUrl.searchParams.get("unread") === "true";
  const limitParam = request.nextUrl.searchParams.get("limit");
  const limit = limitParam ? Number(limitParam) : undefined;

  const notifications = await fetchNotifications(token, firmId, { unreadOnly, limit });
  if (notifications === null) {
    return NextResponse.json({ error: "failed to load notifications" }, { status: 502 });
  }
  return NextResponse.json(notifications);
}
