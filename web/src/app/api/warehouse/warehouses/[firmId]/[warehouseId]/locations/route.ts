// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createLocation, type CreateLocationInput } from "@/lib/warehouse";

type RouteParams = {
  params: Promise<{ firmId: string; warehouseId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/warehouses/{warehouseId}/locations
// (owner-only) - no authorization decision made here:
// internal/warehouse.CreateLocation is the sole place that checks the
// caller actually holds an owner-flagged role.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, warehouseId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateLocationInput>;
  if (!body.code) {
    return NextResponse.json({ error: "missing code" }, { status: 400 });
  }

  const result = await createLocation(token, firmId, warehouseId, {
    code: body.code,
    name: body.name,
    type: body.type,
    parentId: body.parentId,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.location, { status: 201 });
}
