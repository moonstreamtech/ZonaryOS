// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createConnector, type ConnectorInput } from "@/lib/integrations";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/integration-connectors
// (owner-only) - no authorization or config validation decision made
// here: internal/integration.CreateConnector is the sole place that
// checks the caller is an owner and that name/type are structurally
// valid, same convention as every other proxy route (e.g.
// src/app/api/webhooks/[firmId]/route.ts).
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<ConnectorInput>;
  if (!body.name || !body.type) {
    return NextResponse.json({ error: "missing name or type" }, { status: 400 });
  }

  const result = await createConnector(token, firmId, {
    name: body.name,
    type: body.type,
    config: body.config ?? {},
    isActive: body.isActive ?? true,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.value, { status: 201 });
}
