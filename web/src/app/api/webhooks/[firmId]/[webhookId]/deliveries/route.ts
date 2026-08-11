// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchWebhookDeliveries } from "@/lib/webhooks";

type RouteParams = {
  params: Promise<{ firmId: string; webhookId: string }>;
};

// Proxies the Go backend's GET
// /api/firms/{firmId}/webhooks/{webhookId}/deliveries (owner-only) -
// fetched client-side (unlike the initial webhook list, which loads
// server-side in page.tsx) because delivery history is only requested
// when a user expands a specific row.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, webhookId } = await params;
  const deliveries = await fetchWebhookDeliveries(token, firmId, webhookId);
  if (deliveries === null) {
    return NextResponse.json({ error: "failed to load deliveries" }, { status: 400 });
  }
  return NextResponse.json(deliveries);
}
