// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import {
  fetchMaintenanceSchedules,
  createMaintenanceSchedule,
  type CreateMaintenanceScheduleInput,
} from "@/lib/asset";

type RouteParams = {
  params: Promise<{ firmId: string; assetId: string }>;
};

// Proxies the Go backend's GET/POST
// /api/firms/{firmId}/assets/{assetId}/maintenance-schedules. POST is
// owner-only - no authorization decision made here:
// internal/asset.CreateMaintenanceSchedule is the sole place that checks
// the caller actually holds an owner-flagged role, same convention as
// every other mutation proxy route in this codebase.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, assetId } = await params;
  const schedules = await fetchMaintenanceSchedules(token, firmId, assetId);
  if (schedules === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(schedules);
}

export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, assetId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateMaintenanceScheduleInput>;
  if (!body.name || !body.intervalDays) {
    return NextResponse.json({ error: "missing name or intervalDays" }, { status: 400 });
  }

  const result = await createMaintenanceSchedule(token, firmId, assetId, {
    name: body.name,
    intervalDays: body.intervalDays,
    lastDoneAt: body.lastDoneAt,
    assignedToPersonId: body.assignedToPersonId,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.schedule, { status: 201 });
}
