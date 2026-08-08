// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createContract, type CreateContractInput } from "@/lib/hr";

type RouteParams = {
  params: Promise<{ firmId: string; personId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/people/{personId}/contracts
// (owner-only). No authorization decision made here, same convention as
// every other mutation proxy route in this codebase.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, personId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateContractInput>;
  if (!body.type || !body.startDate) {
    return NextResponse.json({ error: "missing type or startDate" }, { status: 400 });
  }

  const result = await createContract(token, firmId, personId, {
    type: body.type,
    amount: body.amount,
    currency: body.currency,
    startDate: body.startDate,
    endDate: body.endDate,
    notes: body.notes,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.contract, { status: 201 });
}
