// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateOpportunityStage, type OpportunityStage } from "@/lib/crm";

type RouteParams = {
  params: Promise<{ firmId: string; opportunityId: string }>;
};

// Proxies the Go backend's POST
// /api/firms/{firmId}/opportunities/{opportunityId}/stage (owner-only) -
// no authorization decision made here: internal/crm.UpdateOpportunityStage
// is the sole place that checks the caller actually holds an
// owner-flagged role. The backend also rejects (400) any change once the
// opportunity is already won/lost (terminal) - that error passes through
// as-is.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, opportunityId } = await params;
  const body = (await request.json().catch(() => ({}))) as { stage?: OpportunityStage };
  if (!body.stage) {
    return NextResponse.json({ error: "missing stage" }, { status: 400 });
  }

  const result = await updateOpportunityStage(token, firmId, opportunityId, body.stage);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.opportunity);
}
