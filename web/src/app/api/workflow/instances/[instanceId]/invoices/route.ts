// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchInstanceInvoices } from "@/lib/workflow";

// Proxies the Go backend's GET .../workflow-instances/{instanceId}/invoices
// (Part 3a) - firmId comes from a query param, same convention this
// proxy layer already uses wherever a GET route needs it and the
// underlying page/component doesn't have it in its own path segment.
export async function GET(
  request: NextRequest,
  { params }: { params: Promise<{ instanceId: string }> },
) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const firmId = request.nextUrl.searchParams.get("firmId");
  if (!firmId) {
    return NextResponse.json({ error: "missing firmId" }, { status: 400 });
  }

  const { instanceId } = await params;
  const invoices = await fetchInstanceInvoices(token, firmId, instanceId);
  if (invoices === null) {
    return NextResponse.json({ error: "failed to load invoices" }, { status: 400 });
  }
  return NextResponse.json(invoices);
}
