// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchOpportunities, createOpportunity, type CreateOpportunityInput } from "@/lib/crm";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET/POST /api/firms/{firmId}/opportunities
// (optional ?customerId= filter on GET). POST is owner-only - no
// authorization decision made here: internal/crm.CreateOpportunity is
// the sole place that checks the caller actually holds an owner-flagged
// role, same convention as every other mutation proxy route in this
// codebase.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const customerId = request.nextUrl.searchParams.get("customerId") ?? undefined;
  const opportunities = await fetchOpportunities(token, firmId, { customerId });
  if (opportunities === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(opportunities);
}

export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateOpportunityInput>;
  if (!body.customerId || !body.name) {
    return NextResponse.json({ error: "missing customerId or name" }, { status: 400 });
  }

  const result = await createOpportunity(token, firmId, {
    customerId: body.customerId,
    name: body.name,
    value: body.value,
    currency: body.currency,
    probability: body.probability,
    expectedCloseDate: body.expectedCloseDate,
    assignedToPersonId: body.assignedToPersonId,
    notes: body.notes,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.opportunity, { status: 201 });
}
