// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createWarehouse, type CreateWarehouseInput } from "@/lib/warehouse";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/warehouses
// (owner-only) - no authorization decision made here:
// internal/warehouse.CreateWarehouse is the sole place that checks the
// caller actually holds an owner-flagged role.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateWarehouseInput>;
  if (!body.name) {
    return NextResponse.json({ error: "missing name" }, { status: 400 });
  }

  const result = await createWarehouse(token, firmId, { name: body.name, address: body.address });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.warehouse, { status: 201 });
}
