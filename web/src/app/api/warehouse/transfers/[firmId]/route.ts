// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchInventoryTransfers, createInventoryTransfer, type CreateInventoryTransferInput } from "@/lib/warehouse";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET /api/firms/{firmId}/inventory-transfers
// (member-gated read) - no authorization decision made here:
// internal/warehouse is the sole place that checks firm membership.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const transfers = await fetchInventoryTransfers(token, firmId);
  if (transfers === null) {
    return NextResponse.json({ error: "failed to load transfers" }, { status: 502 });
  }
  return NextResponse.json(transfers);
}

// Proxies the Go backend's POST /api/firms/{firmId}/inventory-transfers
// (owner-only) - no authorization decision made here:
// internal/warehouse.CreateInventoryTransfer is the sole place that
// checks the caller actually holds an owner-flagged role.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateInventoryTransferInput>;
  if (!body.fromLocationId || !body.toLocationId || !body.productId || !body.quantity) {
    return NextResponse.json(
      { error: "missing fromLocationId, toLocationId, productId, or quantity" },
      { status: 400 },
    );
  }

  const result = await createInventoryTransfer(token, firmId, {
    fromLocationId: body.fromLocationId,
    toLocationId: body.toLocationId,
    productId: body.productId,
    quantity: body.quantity,
    notes: body.notes,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.transfer, { status: 201 });
}
