// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { deleteAIConfig } from "@/lib/aiConfig";

type RouteParams = {
  params: Promise<{ firmId: string; configId: string }>;
};

// Proxies the Go backend's DELETE /api/firms/{firmId}/ai-config/{configId}
// (owner-only). No authorization decision made here, same convention as
// the sibling create route.
export async function DELETE(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, configId } = await params;
  const ok = await deleteAIConfig(token, firmId, configId);
  if (!ok) {
    return NextResponse.json({ error: "failed to delete AI configuration" }, { status: 400 });
  }
  return NextResponse.json({ ok: true });
}
