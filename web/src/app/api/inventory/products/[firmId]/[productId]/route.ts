// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateProduct, type UpdateProductInput } from "@/lib/inventory";

type RouteParams = {
  params: Promise<{ firmId: string; productId: string }>;
};

// Proxies the Go backend's PATCH /api/firms/{firmId}/products/{productId}
// (owner-only) - a partial update. No authorization decision made here,
// same convention as the sibling create route.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, productId } = await params;
  const body = (await request.json().catch(() => ({}))) as UpdateProductInput;

  const result = await updateProduct(token, firmId, productId, body);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.product);
}
