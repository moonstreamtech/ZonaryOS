// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateSalesOrderStatus, type SalesOrderStatus } from "@/lib/salesorders";

type RouteParams = {
  params: Promise<{ firmId: string; orderId: string }>;
};

// Proxies the Go backend's PATCH /api/firms/{firmId}/sales-orders/{orderId}
// (owner-only) - no authorization decision made here:
// internal/salesorders.UpdateSalesOrderStatus is the sole place that
// checks the caller actually holds an owner-flagged role.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, orderId } = await params;
  const body = (await request.json().catch(() => ({}))) as { status?: SalesOrderStatus };
  if (!body.status) {
    return NextResponse.json({ error: "missing status" }, { status: 400 });
  }

  const result = await updateSalesOrderStatus(token, firmId, orderId, body.status);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.salesOrder);
}
