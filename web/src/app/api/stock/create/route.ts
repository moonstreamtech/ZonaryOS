// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createInstance } from "@/lib/workflow";

// Proxies the Go backend's "add stock" endpoint - same cookie-to-Bearer-
// token pattern as src/app/api/stock/[instanceId]/sell/route.ts. No
// authorization decision is made here: internal/workflow.CreateInstance
// is the sole place that checks whether the caller holds the
// definition's create_permission_key - this route only forwards the
// caller's session token to it.
export async function POST(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const body = (await request.json().catch(() => ({}))) as {
    firmId?: string;
    definitionId?: string;
    payload?: Record<string, unknown>;
  };
  if (!body.firmId || !body.definitionId) {
    return NextResponse.json(
      { error: "missing firmId or definitionId" },
      { status: 400 },
    );
  }

  const result = await createInstance(
    token,
    body.firmId,
    body.definitionId,
    body.payload ?? {},
  );
  if (!result.ok) {
    return NextResponse.json(
      { error: result.error },
      { status: result.status || 400 },
    );
  }

  return NextResponse.json(result.instance, { status: 201 });
}
