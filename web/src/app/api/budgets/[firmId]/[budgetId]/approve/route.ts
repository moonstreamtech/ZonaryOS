// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { approveBudget } from "@/lib/budget";

type RouteParams = {
  params: Promise<{ firmId: string; budgetId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/budgets/{budgetId}/approve
// (owner-only, drafts -> approved). No authorization decision made here,
// same convention as the sibling budget routes - internal/
// budget.ApproveBudget is the sole place that checks the caller actually
// holds an owner-flagged role.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, budgetId } = await params;
  const result = await approveBudget(token, firmId, budgetId);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.budget);
}
