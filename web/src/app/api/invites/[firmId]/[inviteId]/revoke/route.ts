// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { revokeInvite } from "@/lib/invite";

type RouteParams = {
  params: Promise<{ firmId: string; inviteId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/invites/{inviteId}/revoke
// (owner-only) - marks a pending invite revoked; internal/invite.Revoke
// never deletes the row, so this is a status transition, not a deletion.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, inviteId } = await params;
  const result = await revokeInvite(token, firmId, inviteId);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return new NextResponse(null, { status: 204 });
}
