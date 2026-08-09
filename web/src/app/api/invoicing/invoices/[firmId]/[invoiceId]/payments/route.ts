// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { recordPayment, type RecordPaymentInput } from "@/lib/invoicing";

type RouteParams = {
  params: Promise<{ firmId: string; invoiceId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/invoices/{invoiceId}/payments
// (owner-only) - no authorization decision made here:
// internal/invoicing.RecordPayment is the sole place that checks the
// caller actually holds an owner-flagged role, and it's also the sole
// place that decides whether this payment closes the invoice and posts
// the DR Cash / CR Trade Receivables journal entry.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, invoiceId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<RecordPaymentInput>;
  if (!body.amount || !body.paidAt) {
    return NextResponse.json({ error: "missing amount or paidAt" }, { status: 400 });
  }

  const result = await recordPayment(token, firmId, invoiceId, {
    amount: body.amount,
    currency: body.currency,
    paidAt: body.paidAt,
    method: body.method,
    reference: body.reference,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.result, { status: 201 });
}
