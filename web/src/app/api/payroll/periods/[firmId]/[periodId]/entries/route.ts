// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createEntry, fetchEntries, type CreateEntryInput } from "@/lib/payroll";

type RouteParams = {
  params: Promise<{ firmId: string; periodId: string }>;
};

// Proxies the Go backend's GET
// /api/firms/{firmId}/payroll-periods/{periodId}/entries (member-gated
// read) - lets client components (PayrollManager's per-period expand)
// fetch entries via the session cookie instead of needing the raw bearer
// token passed down as a prop.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const { firmId, periodId } = await params;
  const entries = await fetchEntries(token, firmId, periodId);
  if (entries === null) {
    return NextResponse.json({ error: "failed to fetch entries" }, { status: 502 });
  }
  return NextResponse.json(entries);
}

// Proxies the Go backend's POST
// /api/firms/{firmId}/payroll-periods/{periodId}/entries (owner-only).
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, periodId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateEntryInput>;
  if (!body.personId || !body.grossAmount || !body.netAmount) {
    return NextResponse.json({ error: "missing personId, grossAmount, or netAmount" }, { status: 400 });
  }

  const result = await createEntry(token, firmId, periodId, {
    personId: body.personId,
    grossAmount: body.grossAmount,
    netAmount: body.netAmount,
    currency: body.currency,
    notes: body.notes,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.entry, { status: 201 });
}
