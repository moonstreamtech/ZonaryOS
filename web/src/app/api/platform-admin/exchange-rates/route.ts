// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createExchangeRate, type CreateExchangeRateInput } from "@/lib/exchangeRates";

// Proxies the Go backend's POST /api/exchange-rates
// (internal/platformadmin.Allowlist-gated) - no authorization decision
// made here, same convention as every other platform-admin proxy route.
export async function POST(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const body = (await request.json().catch(() => ({}))) as Partial<CreateExchangeRateInput>;
  if (!body.fromCurrency || !body.toCurrency || !body.rate || !body.validFrom) {
    return NextResponse.json({ error: "missing required fields" }, { status: 400 });
  }

  const result = await createExchangeRate(token, {
    fromCurrency: body.fromCurrency, toCurrency: body.toCurrency, rate: body.rate, validFrom: body.validFrom, source: body.source,
  });
  if (!result.ok) {
    return NextResponse.json({ error: "failed to create exchange rate" }, { status: result.status || 400 });
  }
  return NextResponse.json(result.rate, { status: 201 });
}
