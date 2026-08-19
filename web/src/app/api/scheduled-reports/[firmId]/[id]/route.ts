// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateScheduledReport, deleteScheduledReport, type ScheduledReportInput } from "@/lib/scheduledreports";

type RouteParams = {
  params: Promise<{ firmId: string; id: string }>;
};

// Proxies the Go backend's PATCH /api/firms/{firmId}/scheduled-reports/{id}
// (owner-only). No authorization decision made here, same convention as
// the sibling create route.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, id } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<ScheduledReportInput>;
  if (!body.name || !body.definitionId || !body.scheduleInterval) {
    return NextResponse.json({ error: "missing name, definitionId, or scheduleInterval" }, { status: 400 });
  }

  const result = await updateScheduledReport(token, firmId, id, {
    definitionId: body.definitionId,
    name: body.name,
    scheduleInterval: body.scheduleInterval,
    recipientUserIds: body.recipientUserIds ?? [],
    isActive: body.isActive ?? true,
  });
  if (!result.ok) {
    return NextResponse.json({ error: "failed to save scheduled report" }, { status: result.status || 400 });
  }
  return NextResponse.json(result.scheduled);
}

// Proxies the Go backend's DELETE /api/firms/{firmId}/scheduled-reports/{id}
// (owner-only, hard delete).
export async function DELETE(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, id } = await params;
  const ok = await deleteScheduledReport(token, firmId, id);
  if (!ok) {
    return NextResponse.json({ error: "failed to delete scheduled report" }, { status: 400 });
  }
  return NextResponse.json({ ok: true });
}
