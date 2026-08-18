// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { testConnector } from "@/lib/integrations";

type RouteParams = {
  params: Promise<{ firmId: string; connectorId: string }>;
};

// Proxies the Go backend's POST
// /api/firms/{firmId}/integration-connectors/{connectorId}/test
// (owner-only) - a plain reachability ping against the connector's own
// config["url"] for http_webhook (internal/integration.TestConnection's
// own doc comment); every other connector type 501s
// (ErrConnectionTestNotImplemented), surfaced to the caller as-is.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, connectorId } = await params;
  const result = await testConnector(token, firmId, connectorId);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json({ ok: true });
}
