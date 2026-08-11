// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { revokeAPIKey } from "@/lib/apikeys";

type RouteParams = {
  params: Promise<{ firmId: string; keyId: string }>;
};

// Proxies the Go backend's DELETE /api/firms/{firmId}/api-keys/{keyId}
// (owner-only, revokes). No authorization decision made here, same
// convention as the sibling create route.
export async function DELETE(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, keyId } = await params;
  const ok = await revokeAPIKey(token, firmId, keyId);
  if (!ok) {
    return NextResponse.json({ error: "failed to revoke API key" }, { status: 400 });
  }
  return NextResponse.json({ ok: true });
}
