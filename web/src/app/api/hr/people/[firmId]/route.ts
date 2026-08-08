// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createPerson, type CreatePersonInput } from "@/lib/hr";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/people (owner-only) -
// no authorization decision made here: internal/hr.CreatePerson is the
// sole place that checks the caller actually holds an owner-flagged
// role, same convention as every other mutation proxy route in this
// codebase.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreatePersonInput>;
  if (!body.fullName || !body.type) {
    return NextResponse.json({ error: "missing fullName or type" }, { status: 400 });
  }

  const result = await createPerson(token, firmId, {
    fullName: body.fullName,
    type: body.type,
    email: body.email,
    phone: body.phone,
    startDate: body.startDate,
    endDate: body.endDate,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.person, { status: 201 });
}
