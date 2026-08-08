// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createCustomer, type CreateCustomerInput } from "@/lib/crm";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/customers (owner-only) -
// no authorization decision made here: internal/crm.CreateCustomer is the
// sole place that checks the caller actually holds an owner-flagged role,
// same convention as every other mutation proxy route in this codebase.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateCustomerInput>;
  if (!body.name) {
    return NextResponse.json({ error: "missing name" }, { status: 400 });
  }

  const result = await createCustomer(token, firmId, {
    name: body.name,
    email: body.email,
    phone: body.phone,
    address: body.address,
    taxId: body.taxId,
    creditLimit: body.creditLimit,
    currency: body.currency,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.customer, { status: 201 });
}
