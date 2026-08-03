// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createInstance } from "@/lib/workflow";
import { recordFeatureEvent } from "@/lib/telemetryServer";

// Generic replacement for the old stock-specific /api/stock/create route:
// proxies the Go backend's "create instance" endpoint for any workflow
// definition, not just stock_to_sale - same cookie-to-Bearer-token
// pattern as every other proxy route. No authorization decision is made
// here: internal/workflow.CreateInstance is the sole place that checks
// whether the caller holds the definition's create_permission_key.
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

  // Telemetry (Open Points item 40): counts an already-succeeded action
  // the route handler just observed - no new instrumentation reading
  // business data, see lib/telemetryServer.ts's doc comment. No-op
  // unless ZONARYOS_TELEMETRY_ENABLED=true.
  recordFeatureEvent("workflow_instance_created");

  return NextResponse.json(result.instance, { status: 201 });
}
