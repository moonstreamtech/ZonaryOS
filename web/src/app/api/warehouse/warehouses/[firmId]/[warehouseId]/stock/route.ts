// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchWarehouseStock } from "@/lib/warehouse";

type RouteParams = {
  params: Promise<{ firmId: string; warehouseId: string }>;
};

// Proxies the Go backend's GET /api/firms/{firmId}/warehouses/{warehouseId}/stock
// (member-gated read) - no authorization decision made here:
// internal/warehouse is the sole place that checks firm membership.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, warehouseId } = await params;
  const stock = await fetchWarehouseStock(token, firmId, warehouseId);
  if (stock === null) {
    return NextResponse.json({ error: "failed to load stock" }, { status: 502 });
  }
  return NextResponse.json(stock);
}
