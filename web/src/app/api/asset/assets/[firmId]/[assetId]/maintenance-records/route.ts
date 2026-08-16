// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import {
  fetchMaintenanceRecords,
  createMaintenanceRecord,
  type CreateMaintenanceRecordInput,
} from "@/lib/asset";

type RouteParams = {
  params: Promise<{ firmId: string; assetId: string }>;
};

// Proxies the Go backend's GET/POST
// /api/firms/{firmId}/assets/{assetId}/maintenance-records. POST is
// owner-only - no authorization decision made here:
// internal/asset.CreateMaintenanceRecord is the sole place that checks
// the caller actually holds an owner-flagged role, same convention as
// every other mutation proxy route in this codebase.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, assetId } = await params;
  const records = await fetchMaintenanceRecords(token, firmId, assetId);
  if (records === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(records);
}

export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, assetId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateMaintenanceRecordInput>;
  if (!body.performedAt || !body.description) {
    return NextResponse.json({ error: "missing performedAt or description" }, { status: 400 });
  }

  const result = await createMaintenanceRecord(token, firmId, assetId, {
    scheduleId: body.scheduleId,
    performedAt: body.performedAt,
    performedByPersonId: body.performedByPersonId,
    description: body.description,
    cost: body.cost,
    nextServiceDate: body.nextServiceDate,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.record, { status: 201 });
}
