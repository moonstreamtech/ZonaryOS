// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchInventoryTransfer } from "@/lib/warehouse";

type RouteParams = {
  params: Promise<{ firmId: string; transferId: string }>;
};

// Proxies the Go backend's GET /api/firms/{firmId}/inventory-transfers/{transferId}
// (member-gated read) - no authorization decision made here:
// internal/warehouse is the sole place that checks firm membership.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, transferId } = await params;
  const transfer = await fetchInventoryTransfer(token, firmId, transferId);
  if (transfer === null) {
    return NextResponse.json({ error: "not found" }, { status: 404 });
  }
  return NextResponse.json(transfer);
}
