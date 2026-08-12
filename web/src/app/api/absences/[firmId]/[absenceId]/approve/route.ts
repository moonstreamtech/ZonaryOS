// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { approveAbsence } from "@/lib/absence";

type RouteParams = {
  params: Promise<{ firmId: string; absenceId: string }>;
};

// Proxies the Go backend's PATCH /api/firms/{firmId}/absences/{id}/approve
// (owner-only) - internal/absence.Approve executes the real
// workflow.absence_approval "approve" transition, then syncs
// absences.status.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, absenceId } = await params;
  const result = await approveAbsence(token, firmId, absenceId);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.absence);
}
