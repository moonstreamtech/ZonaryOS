// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchAssets, createAsset, type CreateAssetInput } from "@/lib/asset";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET/POST /api/firms/{firmId}/assets. POST is
// owner-only - no authorization decision made here:
// internal/asset.CreateAsset is the sole place that checks the caller
// actually holds an owner-flagged role, same convention as every other
// mutation proxy route in this codebase.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const assets = await fetchAssets(token, firmId);
  if (assets === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(assets);
}

export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateAssetInput>;
  if (!body.name || !body.assetNumber || !body.type) {
    return NextResponse.json({ error: "missing name, assetNumber, or type" }, { status: 400 });
  }

  const result = await createAsset(token, firmId, {
    name: body.name,
    assetNumber: body.assetNumber,
    type: body.type,
    status: body.status,
    locationId: body.locationId,
    assignedToPersonId: body.assignedToPersonId,
    purchaseDate: body.purchaseDate,
    purchasePrice: body.purchasePrice,
    currentValue: body.currentValue,
    depreciationRate: body.depreciationRate,
    serialNumber: body.serialNumber,
    warrantyExpires: body.warrantyExpires,
    notes: body.notes,
    customFields: body.customFields,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.asset, { status: 201 });
}
