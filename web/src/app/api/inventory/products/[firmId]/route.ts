// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createProduct, type CreateProductInput } from "@/lib/inventory";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/products (owner-only) -
// no authorization decision made here: internal/inventory.CreateProduct is
// the sole place that checks the caller actually holds an owner-flagged
// role, same convention as every other mutation proxy route in this
// codebase (e.g. api/hr/people/[firmId]/route.ts).
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateProductInput>;
  if (!body.sku || !body.name) {
    return NextResponse.json({ error: "missing sku or name" }, { status: 400 });
  }

  const result = await createProduct(token, firmId, {
    sku: body.sku,
    name: body.name,
    description: body.description,
    unit: body.unit,
    unitPrice: body.unitPrice,
    costPrice: body.costPrice,
    taxRate: body.taxRate,
    category: body.category,
    minQuantity: body.minQuantity,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.product, { status: 201 });
}
