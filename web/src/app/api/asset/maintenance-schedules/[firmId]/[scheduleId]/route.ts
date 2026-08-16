// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateMaintenanceSchedule, type UpdateMaintenanceScheduleInput } from "@/lib/asset";

type RouteParams = {
  params: Promise<{ firmId: string; scheduleId: string }>;
};

// Proxies the Go backend's PATCH
// /api/firms/{firmId}/maintenance-schedules/{scheduleId} (owner-only) -
// no authorization decision made here:
// internal/asset.UpdateMaintenanceSchedule is the sole place that checks
// the caller actually holds an owner-flagged role.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, scheduleId } = await params;
  const body = (await request.json().catch(() => ({}))) as UpdateMaintenanceScheduleInput;

  const result = await updateMaintenanceSchedule(token, firmId, scheduleId, body);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.schedule);
}
