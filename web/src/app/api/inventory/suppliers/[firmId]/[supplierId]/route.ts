// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateSupplier, type UpdateSupplierInput } from "@/lib/inventory";

type RouteParams = {
  params: Promise<{ firmId: string; supplierId: string }>;
};

// Proxies the Go backend's PATCH /api/firms/{firmId}/suppliers/{supplierId}
// (owner-only) - a partial update. No authorization decision made here,
// same convention as the sibling create route.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, supplierId } = await params;
  const body = (await request.json().catch(() => ({}))) as UpdateSupplierInput;

  const result = await updateSupplier(token, firmId, supplierId, body);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.supplier);
}
