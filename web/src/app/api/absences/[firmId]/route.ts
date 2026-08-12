// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createAbsence, type CreateAbsenceInput } from "@/lib/absence";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/absences (member-
// gated - any member can request their own absence, see
// internal/absence.CreateAbsence's own doc comment).
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateAbsenceInput>;
  if (!body.personId || !body.type || !body.startDate || !body.endDate) {
    return NextResponse.json({ error: "missing personId, type, startDate, or endDate" }, { status: 400 });
  }

  const result = await createAbsence(token, firmId, {
    personId: body.personId,
    type: body.type,
    startDate: body.startDate,
    endDate: body.endDate,
    notes: body.notes,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.absence, { status: 201 });
}
