// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createInvoice, type CreateInvoiceInput } from "@/lib/invoicing";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/invoices (owner-only) -
// no authorization decision made here: internal/invoicing.CreateInvoice is
// the sole place that checks the caller actually holds an owner-flagged
// role, same convention as every other mutation proxy route in this
// codebase.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateInvoiceInput>;
  if (!body.issuedDate || !body.lines || body.lines.length === 0) {
    return NextResponse.json({ error: "missing issuedDate or lines" }, { status: 400 });
  }

  const result = await createInvoice(token, firmId, {
    customerId: body.customerId,
    issuedDate: body.issuedDate,
    dueDate: body.dueDate,
    currency: body.currency,
    notes: body.notes,
    lines: body.lines,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.invoice, { status: 201 });
}
