// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchBudgets, createBudget, type CreateBudgetInput } from "@/lib/budget";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET/POST /api/firms/{firmId}/budgets. POST is
// owner-only - no authorization decision made here:
// internal/budget.CreateBudget is the sole place that checks the caller
// actually holds an owner-flagged role, same convention as every other
// mutation proxy route in this codebase.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const budgets = await fetchBudgets(token, firmId);
  if (budgets === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(budgets);
}

export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateBudgetInput>;
  if (!body.name || !body.periodType || !body.periodStart || !body.periodEnd) {
    return NextResponse.json(
      { error: "missing name, periodType, periodStart, or periodEnd" },
      { status: 400 },
    );
  }

  const result = await createBudget(token, firmId, {
    name: body.name,
    periodType: body.periodType,
    periodStart: body.periodStart,
    periodEnd: body.periodEnd,
    status: body.status,
    currency: body.currency,
    notes: body.notes,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.budget, { status: 201 });
}
