// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { acceptInvite } from "@/lib/invite";

type RouteParams = {
  params: Promise<{ token: string }>;
};

// Proxies the Go backend's POST /api/invites/{token}/accept - requires the
// visitor to already be signed in (a zonaryos_session cookie), but
// deliberately not firm membership - see internal/invite/handlers.go's
// RegisterRoutes doc comment. Kept at a distinct /api/invite-accept/...
// path (not nested under /api/invites/[firmId]/...) since this route has
// no firmId at all - the firm is derived entirely from the token itself.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const sessionToken = request.cookies.get("zonaryos_session")?.value;
  if (!sessionToken) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { token } = await params;
  const result = await acceptInvite(sessionToken, token);
  if (!result.ok) {
    return NextResponse.json({ error: "accept failed" }, { status: result.httpStatus || 400 });
  }
  return NextResponse.json({ firmId: result.firmId, roleName: result.roleName });
}
