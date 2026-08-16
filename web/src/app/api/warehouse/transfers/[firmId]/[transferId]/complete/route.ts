// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { completeInventoryTransfer } from "@/lib/warehouse";

type RouteParams = {
  params: Promise<{ firmId: string; transferId: string }>;
};

// Proxies the Go backend's POST .../inventory-transfers/{transferId}/complete
// (owner-only) - no authorization decision made here:
// internal/warehouse.CompleteInventoryTransfer is the sole place that
// checks the caller actually holds an owner-flagged role. Can fail with a
// plain-text 400 body (e.g. "insufficient stock: ...") - relayed to the
// caller as-is via result.error.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, transferId } = await params;
  const result = await completeInventoryTransfer(token, firmId, transferId);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.transfer);
}
