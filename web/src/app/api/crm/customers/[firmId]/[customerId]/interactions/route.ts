// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchInteractions, createInteraction, type CreateInteractionInput } from "@/lib/crm";

type RouteParams = {
  params: Promise<{ firmId: string; customerId: string }>;
};

// Proxies the Go backend's GET/POST
// /api/firms/{firmId}/customers/{customerId}/interactions. POST is
// owner-only - no authorization decision made here:
// internal/crm.CreateInteraction is the sole place that checks the
// caller actually holds an owner-flagged role, same convention as every
// other mutation proxy route in this codebase.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, customerId } = await params;
  const interactions = await fetchInteractions(token, firmId, customerId);
  if (interactions === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(interactions);
}

export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, customerId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateInteractionInput>;
  if (!body.type || !body.date || !body.summary) {
    return NextResponse.json({ error: "missing type, date, or summary" }, { status: 400 });
  }

  const result = await createInteraction(token, firmId, customerId, {
    type: body.type,
    date: body.date,
    summary: body.summary,
    outcome: body.outcome,
    nextAction: body.nextAction,
    nextActionDate: body.nextActionDate,
    opportunityId: body.opportunityId,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.interaction, { status: 201 });
}
