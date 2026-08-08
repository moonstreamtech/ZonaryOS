// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateInvoiceStatus, type InvoiceStatus } from "@/lib/invoicing";

type RouteParams = {
  params: Promise<{ firmId: string; invoiceId: string }>;
};

// Proxies the Go backend's PATCH /api/firms/{firmId}/invoices/{invoiceId}
// (owner-only) - no authorization decision made here:
// internal/invoicing.UpdateInvoiceStatus is the sole place that checks the
// caller actually holds an owner-flagged role, and it's also the sole
// place that enforces the allowed-transitions state machine (this route
// just forwards whatever status string it's given).
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, invoiceId } = await params;
  const body = (await request.json().catch(() => ({}))) as { status?: InvoiceStatus };
  if (!body.status) {
    return NextResponse.json({ error: "missing status" }, { status: 400 });
  }

  const result = await updateInvoiceStatus(token, firmId, invoiceId, body.status);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.invoice);
}
