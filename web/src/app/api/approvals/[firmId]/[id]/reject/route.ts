// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { rejectApproval } from "@/lib/approvals";

type RouteParams = {
  params: Promise<{ firmId: string; id: string }>;
};

// Proxies the Go backend's POST .../pending-approvals/{id}/reject - same
// convention as the sibling approve route.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, id } = await params;
  const result = await rejectApproval(token, firmId, id);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.approval);
}
