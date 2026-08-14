// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createProductionOrder, type CreateProductionOrderInput } from "@/lib/manufacturing";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/production-orders
// (owner-only) - no authorization decision made here:
// internal/manufacturing.CreateProductionOrder is the sole place that
// checks the caller actually holds an owner-flagged role.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateProductionOrderInput>;
  if (!body.productId || !body.bomId || !body.quantityPlanned) {
    return NextResponse.json({ error: "missing productId, bomId, or quantityPlanned" }, { status: 400 });
  }

  const result = await createProductionOrder(token, firmId, {
    productId: body.productId,
    bomId: body.bomId,
    quantityPlanned: body.quantityPlanned,
    plannedStart: body.plannedStart,
    plannedEnd: body.plannedEnd,
    notes: body.notes,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.productionOrder, { status: 201 });
}
