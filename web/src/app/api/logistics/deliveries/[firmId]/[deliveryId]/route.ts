// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateDelivery, type UpdateDeliveryInput } from "@/lib/logistics";

type RouteParams = {
  params: Promise<{ firmId: string; deliveryId: string }>;
};

// Proxies the Go backend's PATCH /api/firms/{firmId}/deliveries/{deliveryId}
// (owner-only) - a partial update. No authorization decision made here,
// same convention as the sibling create route.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, deliveryId } = await params;
  const body = (await request.json().catch(() => ({}))) as UpdateDeliveryInput;

  const result = await updateDelivery(token, firmId, deliveryId, body);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.delivery);
}
