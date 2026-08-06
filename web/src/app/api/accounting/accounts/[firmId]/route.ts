// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createAccount, type CreateAccountInput } from "@/lib/accounting";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/accounts (owner-only)
// - no authorization decision made here: internal/accounting.CreateAccount
// is the sole place that checks the caller actually holds an
// owner-flagged role, same convention as every other mutation proxy route
// in this codebase (e.g. api/permission-audit/[firmId]/roles).
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateAccountInput>;
  if (!body.code || !body.name || !body.type) {
    return NextResponse.json({ error: "missing code, name, or type" }, { status: 400 });
  }

  const result = await createAccount(token, firmId, {
    code: body.code,
    name: body.name,
    type: body.type,
    parentId: body.parentId,
    currency: body.currency,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.account, { status: 201 });
}
