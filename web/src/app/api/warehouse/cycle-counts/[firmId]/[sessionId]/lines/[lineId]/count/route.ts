// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { recordCycleCountLine } from "@/lib/warehouse";

type RouteParams = {
  params: Promise<{ firmId: string; sessionId: string; lineId: string }>;
};

// Proxies the Go backend's
// POST .../cycle-counts/{sessionId}/lines/{lineId}/count (owner-only) -
// no authorization decision made here: internal/warehouse.RecordCycleCountLine
// is the sole place that checks the caller actually holds an owner-flagged
// role.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, sessionId, lineId } = await params;
  const body = (await request.json().catch(() => ({}))) as { countedQuantity?: string };
  if (!body.countedQuantity) {
    return NextResponse.json({ error: "missing countedQuantity" }, { status: 400 });
  }

  const result = await recordCycleCountLine(token, firmId, sessionId, lineId, body.countedQuantity);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.line);
}
