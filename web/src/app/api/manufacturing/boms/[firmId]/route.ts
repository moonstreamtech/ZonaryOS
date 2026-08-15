// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createBOM, type CreateBOMInput } from "@/lib/manufacturing";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/boms (owner-only) -
// no authorization decision made here: internal/manufacturing.CreateBOM
// is the sole place that checks the caller actually holds an
// owner-flagged role.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateBOMInput>;
  if (!body.productId || !body.name || !body.lines || body.lines.length === 0) {
    return NextResponse.json({ error: "missing productId, name, or lines" }, { status: 400 });
  }

  const result = await createBOM(token, firmId, {
    productId: body.productId,
    name: body.name,
    version: body.version,
    isActive: body.isActive,
    unitYield: body.unitYield,
    notes: body.notes,
    lines: body.lines,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.bom, { status: 201 });
}
