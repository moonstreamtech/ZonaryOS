// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateLocation } from "@/lib/warehouse";

type RouteParams = {
  params: Promise<{ firmId: string; locationId: string }>;
};

// Proxies the Go backend's PATCH /api/firms/{firmId}/locations/{locationId}
// (owner-only) - no authorization decision made here:
// internal/warehouse.UpdateLocation is the sole place that checks the
// caller actually holds an owner-flagged role.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, locationId } = await params;
  const body = (await request.json().catch(() => ({}))) as {
    name?: string;
    type?: string;
    isActive?: boolean;
  };

  const result = await updateLocation(token, firmId, locationId, body as never);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.location);
}
