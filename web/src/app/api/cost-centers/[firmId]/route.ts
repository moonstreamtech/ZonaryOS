// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchCostCenters, createCostCenter, type CreateCostCenterInput } from "@/lib/costcenter";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET/POST /api/firms/{firmId}/cost-centers.
// POST is owner-only - no authorization decision made here:
// internal/costcenter.CreateCostCenter is the sole place that checks the
// caller actually holds an owner-flagged role, same convention as every
// other mutation proxy route in this codebase.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const costCenters = await fetchCostCenters(token, firmId);
  if (costCenters === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(costCenters);
}

export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateCostCenterInput>;
  if (!body.code || !body.name) {
    return NextResponse.json({ error: "missing code or name" }, { status: 400 });
  }

  const result = await createCostCenter(token, firmId, {
    code: body.code,
    name: body.name,
    parentId: body.parentId,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.costCenter, { status: 201 });
}
