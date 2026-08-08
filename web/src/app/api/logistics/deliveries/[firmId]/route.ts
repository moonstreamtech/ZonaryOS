// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createDelivery, type CreateDeliveryInput } from "@/lib/logistics";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/deliveries (owner-only) -
// no authorization decision made here: internal/logistics.CreateDelivery is
// the sole place that checks the caller actually holds an owner-flagged
// role, same convention as every other mutation proxy route in this
// codebase.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateDeliveryInput>;
  if (!body.destinationAddress) {
    return NextResponse.json({ error: "missing destinationAddress" }, { status: 400 });
  }

  const result = await createDelivery(token, firmId, {
    reference: body.reference,
    originAddress: body.originAddress,
    destinationAddress: body.destinationAddress,
    carrier: body.carrier,
    trackingNumber: body.trackingNumber,
    estimatedDate: body.estimatedDate,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.delivery, { status: 201 });
}
