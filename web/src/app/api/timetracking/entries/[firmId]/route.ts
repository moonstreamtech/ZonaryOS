// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createTimeEntry, type CreateTimeEntryInput } from "@/lib/timetracking";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/time-entries
// (member-gated - any member may log time for a person; see
// internal/timetracking's own doc comment for the edit-restriction rule
// that applies afterwards, not at creation time).
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateTimeEntryInput>;
  if (!body.personId || !body.date || !body.hours) {
    return NextResponse.json({ error: "missing personId, date, or hours" }, { status: 400 });
  }

  const result = await createTimeEntry(token, firmId, {
    personId: body.personId,
    date: body.date,
    hours: body.hours,
    description: body.description,
    billable: body.billable,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.entry, { status: 201 });
}
