// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchBudgetLines, createBudgetLine, type CreateBudgetLineInput } from "@/lib/budget";

type RouteParams = {
  params: Promise<{ firmId: string; budgetId: string }>;
};

// Proxies the Go backend's GET/POST /api/firms/{firmId}/budgets/{budgetId}/lines.
// POST is owner-only - no authorization decision made here:
// internal/budget.CreateBudgetLine is the sole place that checks the
// caller actually holds an owner-flagged role, same convention as every
// other mutation proxy route in this codebase.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, budgetId } = await params;
  const lines = await fetchBudgetLines(token, firmId, budgetId);
  if (lines === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(lines);
}

export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, budgetId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateBudgetLineInput>;
  if (!body.accountId || !body.plannedAmount) {
    return NextResponse.json({ error: "missing accountId or plannedAmount" }, { status: 400 });
  }

  const result = await createBudgetLine(token, firmId, budgetId, {
    accountId: body.accountId,
    plannedAmount: body.plannedAmount,
    notes: body.notes,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.line, { status: 201 });
}
