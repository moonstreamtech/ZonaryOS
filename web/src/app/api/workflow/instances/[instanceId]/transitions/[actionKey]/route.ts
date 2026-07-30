// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { executeTransition } from "@/lib/workflow";

// Generic replacement for the old stock-specific /api/stock/[instanceId]/sell
// route: proxies the Go backend's transition endpoint for any action on
// any workflow instance, not just "record_sale" - actionKey comes from
// the route itself (whatever AvailableAction.actionKey the instance
// listing returned), not a hardcoded literal. No authorization decision
// is made here: internal/workflow.ExecuteTransition is the sole place
// that checks whether the caller holds the transition's own
// permission_key.
export async function POST(
  request: NextRequest,
  { params }: { params: Promise<{ instanceId: string; actionKey: string }> },
) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { instanceId, actionKey } = await params;
  const body = (await request.json().catch(() => ({}))) as {
    firmId?: string;
    payload?: Record<string, unknown>;
  };
  if (!body.firmId) {
    return NextResponse.json({ error: "missing firmId" }, { status: 400 });
  }

  const result = await executeTransition(
    token,
    body.firmId,
    instanceId,
    actionKey,
    body.payload ?? {},
  );
  if (!result.ok) {
    return NextResponse.json(
      { error: result.error },
      { status: result.status || 400 },
    );
  }

  return NextResponse.json(result.instance);
}
