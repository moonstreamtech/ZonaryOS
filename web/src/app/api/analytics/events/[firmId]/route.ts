// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { ingestEvents, type EventInput } from "@/lib/analytics";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/analytics/events
// (member-gated). This is the fire-and-forget beacon target for
// components/Analytics/PageViewTracker.tsx (and any other client-side
// event tracking added later) - always answers 202 regardless of the
// backend's own outcome, same "a beacon must never surface as a visible
// error" convention as src/app/api/telemetry/route.ts, since a dropped
// analytics event should never disrupt navigation. No authorization or
// event-vocabulary validation decision made here: internal/analytics.
// IngestEvents is the sole place that checks firm membership and event
// batch shape, same convention as every other proxy route.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ ok: true }, { status: 202 });
  }

  const { firmId } = await params;
  const events = (await request.json().catch(() => [])) as EventInput[];
  if (Array.isArray(events) && events.length > 0) {
    await ingestEvents(token, firmId, events);
  }

  return NextResponse.json({ ok: true }, { status: 202 });
}
