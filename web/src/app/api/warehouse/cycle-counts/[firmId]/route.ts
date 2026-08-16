// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchCycleCounts, createCycleCount, type CreateCycleCountInput } from "@/lib/warehouse";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET /api/firms/{firmId}/cycle-counts
// (member-gated read, no lines) - no authorization decision made here:
// internal/warehouse is the sole place that checks firm membership.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const sessions = await fetchCycleCounts(token, firmId);
  if (sessions === null) {
    return NextResponse.json({ error: "failed to load cycle counts" }, { status: 502 });
  }
  return NextResponse.json(sessions);
}

// Proxies the Go backend's POST /api/firms/{firmId}/cycle-counts
// (owner-only) - no authorization decision made here:
// internal/warehouse.CreateCycleCount is the sole place that checks the
// caller actually holds an owner-flagged role.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateCycleCountInput>;
  if (!body.warehouseId || !body.lines || body.lines.length === 0) {
    return NextResponse.json({ error: "missing warehouseId or lines" }, { status: 400 });
  }

  const result = await createCycleCount(token, firmId, {
    warehouseId: body.warehouseId,
    lines: body.lines,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.session, { status: 201 });
}
