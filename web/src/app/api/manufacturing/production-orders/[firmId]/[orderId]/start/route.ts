// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { startProductionOrder } from "@/lib/manufacturing";

type RouteParams = {
  params: Promise<{ firmId: string; orderId: string }>;
};

// Proxies the Go backend's POST .../production-orders/{orderId}/start
// (owner-only) - no authorization decision made here:
// internal/manufacturing.StartProductionOrder is the sole place that
// checks the caller actually holds an owner-flagged role.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, orderId } = await params;
  const result = await startProductionOrder(token, firmId, orderId);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.productionOrder);
}
