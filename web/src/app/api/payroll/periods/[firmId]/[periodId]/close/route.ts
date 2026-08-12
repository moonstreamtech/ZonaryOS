// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { closePeriod } from "@/lib/payroll";

type RouteParams = {
  params: Promise<{ firmId: string; periodId: string }>;
};

// Proxies the Go backend's POST
// /api/firms/{firmId}/payroll-periods/{periodId}/close (owner-only) -
// transitions the period to 'closed' and posts the DR Salary Expense /
// CR Salaries Payable journal entry atomically (internal/payroll.ClosePeriod).
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, periodId } = await params;
  const result = await closePeriod(token, firmId, periodId);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.period);
}
