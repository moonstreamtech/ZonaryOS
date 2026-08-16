// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateWarehouse } from "@/lib/warehouse";

type RouteParams = {
  params: Promise<{ firmId: string; warehouseId: string }>;
};

// Proxies the Go backend's PATCH /api/firms/{firmId}/warehouses/{warehouseId}
// (owner-only) - no authorization decision made here:
// internal/warehouse.UpdateWarehouse is the sole place that checks the
// caller actually holds an owner-flagged role.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, warehouseId } = await params;
  const body = (await request.json().catch(() => ({}))) as {
    name?: string;
    address?: string;
    isActive?: boolean;
  };

  const result = await updateWarehouse(token, firmId, warehouseId, body);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.warehouse);
}
