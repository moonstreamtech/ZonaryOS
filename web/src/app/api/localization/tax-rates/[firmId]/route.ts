// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createTaxRate, type TaxRateInput } from "@/lib/localization";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/tax-rates
// (owner-only) - no authorization decision made here, same convention as
// the sibling addresses proxy route.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<TaxRateInput>;
  if (!body.name || !body.rate) {
    return NextResponse.json({ error: "missing name or rate" }, { status: 400 });
  }

  const result = await createTaxRate(token, firmId, {
    name: body.name,
    rate: body.rate,
    countryCode: body.countryCode,
    region: body.region,
    appliesTo: body.appliesTo,
    isDefault: body.isDefault ?? false,
  });
  if (!result.ok) {
    return NextResponse.json({ error: "failed to create tax rate" }, { status: result.status || 400 });
  }
  return NextResponse.json(result.taxRate, { status: 201 });
}
