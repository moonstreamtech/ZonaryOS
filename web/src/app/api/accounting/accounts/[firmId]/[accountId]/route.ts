// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateAccount, type UpdateAccountInput } from "@/lib/accounting";

type RouteParams = {
  params: Promise<{ firmId: string; accountId: string }>;
};

// Proxies the Go backend's PATCH /api/firms/{firmId}/accounts/{accountId}
// (owner-only) - a partial update (name and/or isActive). No
// authorization decision made here, same convention as the sibling
// create route.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, accountId } = await params;
  const body = (await request.json().catch(() => ({}))) as UpdateAccountInput;

  const result = await updateAccount(token, firmId, accountId, body);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.account);
}
