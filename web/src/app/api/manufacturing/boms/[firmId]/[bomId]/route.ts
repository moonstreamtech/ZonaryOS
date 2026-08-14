// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateBOM } from "@/lib/manufacturing";

type RouteParams = {
  params: Promise<{ firmId: string; bomId: string }>;
};

// Proxies the Go backend's PATCH /api/firms/{firmId}/boms/{bomId}
// (owner-only) - no authorization decision made here:
// internal/manufacturing.UpdateBOM is the sole place that checks the
// caller actually holds an owner-flagged role. Used for both plain field
// edits and the "set active" version-management action (isActive: true
// deactivates every other version for the same product server-side).
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, bomId } = await params;
  const body = (await request.json().catch(() => ({}))) as { name?: string; notes?: string; isActive?: boolean };

  const result = await updateBOM(token, firmId, bomId, body);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.bom);
}
