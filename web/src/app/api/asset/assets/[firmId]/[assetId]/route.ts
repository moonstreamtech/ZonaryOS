// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchAsset, updateAsset, type UpdateAssetInput } from "@/lib/asset";

type RouteParams = {
  params: Promise<{ firmId: string; assetId: string }>;
};

// Proxies the Go backend's GET/PATCH /api/firms/{firmId}/assets/{assetId}.
// PATCH is owner-only - no authorization decision made here:
// internal/asset.UpdateAsset is the sole place that checks the caller
// actually holds an owner-flagged role, same convention as every other
// mutation proxy route in this codebase.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, assetId } = await params;
  const asset = await fetchAsset(token, firmId, assetId);
  if (asset === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(asset);
}

export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, assetId } = await params;
  const body = (await request.json().catch(() => ({}))) as UpdateAssetInput;

  const result = await updateAsset(token, firmId, assetId, body);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.asset);
}
