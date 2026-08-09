// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createAddress, type AddressInput } from "@/lib/localization";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/addresses
// (owner-only) - no authorization decision made here: internal/
// localization.CreateAddress is the sole place that checks the caller
// actually holds an owner-flagged role, same convention as the sibling
// accounting/accounts proxy route.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<AddressInput>;
  if (!body.line1 || !body.city || !body.countryCode) {
    return NextResponse.json({ error: "missing line1, city, or countryCode" }, { status: 400 });
  }

  const result = await createAddress(token, firmId, {
    label: body.label,
    line1: body.line1,
    line2: body.line2,
    city: body.city,
    stateProvince: body.stateProvince,
    postalCode: body.postalCode,
    countryCode: body.countryCode,
    isDefault: body.isDefault ?? false,
  });
  if (!result.ok) {
    return NextResponse.json({ error: "failed to create address" }, { status: result.status || 400 });
  }
  return NextResponse.json(result.address, { status: 201 });
}
