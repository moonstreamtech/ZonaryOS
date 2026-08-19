// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createScheduledReport, type ScheduledReportInput } from "@/lib/scheduledreports";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/scheduled-reports
// (owner-only) - no authorization or schedule-interval validation
// decision made here: internal/scheduledreports.CreateScheduledReport is
// the sole place that checks the caller is an owner and that the
// scheduleInterval is one of daily/weekly/monthly, same convention as
// every other proxy route.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<ScheduledReportInput>;
  if (!body.name || !body.definitionId || !body.scheduleInterval) {
    return NextResponse.json({ error: "missing name, definitionId, or scheduleInterval" }, { status: 400 });
  }

  const result = await createScheduledReport(token, firmId, {
    definitionId: body.definitionId,
    name: body.name,
    scheduleInterval: body.scheduleInterval,
    recipientUserIds: body.recipientUserIds ?? [],
    isActive: body.isActive ?? true,
  });
  if (!result.ok) {
    return NextResponse.json({ error: "failed to create scheduled report" }, { status: result.status || 400 });
  }
  return NextResponse.json(result.scheduled, { status: 201 });
}
