// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { completeCycleCount } from "@/lib/warehouse";

type RouteParams = {
  params: Promise<{ firmId: string; sessionId: string }>;
};

// Proxies the Go backend's POST .../cycle-counts/{sessionId}/complete
// (owner-only) - no authorization decision made here:
// internal/warehouse.CompleteCycleCount is the sole place that checks the
// caller actually holds an owner-flagged role. `adjust: true` applies a
// real stock adjustment plus a balanced journal entry per line with a
// recorded, non-zero variance, server-side.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, sessionId } = await params;
  const body = (await request.json().catch(() => ({}))) as { adjust?: boolean };

  const result = await completeCycleCount(token, firmId, sessionId, body.adjust === true);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.session);
}
