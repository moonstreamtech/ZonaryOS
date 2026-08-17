// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { postIntercompanyTransfer, type IntercompanyTransferInput } from "@/lib/consolidation";

type RouteParams = {
  params: Promise<{ groupId: string }>;
};

// Proxies the Go backend's
// POST /api/platform-admin/firm-groups/{groupId}/intercompany-transfer
// (internal/platformadmin.Allowlist-gated) - no authorization decision
// made here, same convention as every other platform-admin proxy route.
// The backend posts a real journal entry into both member firms
// atomically; a 400 here (malformed amount/currency/description, or
// either firm not a member of this group) is passed straight through to
// the caller, same as every other write proxy route's error handling.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { groupId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<IntercompanyTransferInput>;
  if (!body.fromFirmId || !body.toFirmId || !body.amount || !body.currency || !body.description) {
    return NextResponse.json(
      { error: "missing required field: fromFirmId, toFirmId, amount, currency, or description" },
      { status: 400 },
    );
  }

  const result = await postIntercompanyTransfer(token, groupId, {
    fromFirmId: body.fromFirmId,
    toFirmId: body.toFirmId,
    amount: body.amount,
    currency: body.currency,
    description: body.description,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.transaction, { status: 201 });
}
