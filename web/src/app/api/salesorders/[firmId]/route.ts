// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createSalesOrder, type CreateSalesOrderInput } from "@/lib/salesorders";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/sales-orders
// (owner-only) - no authorization decision made here:
// internal/salesorders.CreateSalesOrder is the sole place that checks the
// caller actually holds an owner-flagged role, same convention
// web/src/app/api/invoicing/invoices/[firmId]/route.ts's own POST handler
// follows.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateSalesOrderInput>;
  if (!body.lines || body.lines.length === 0) {
    return NextResponse.json({ error: "missing lines" }, { status: 400 });
  }

  const result = await createSalesOrder(token, firmId, {
    customerId: body.customerId,
    shippingAddress: body.shippingAddress,
    notes: body.notes,
    currency: body.currency,
    lines: body.lines,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.salesOrder, { status: 201 });
}
