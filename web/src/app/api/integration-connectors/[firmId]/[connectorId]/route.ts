// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateConnector, deleteConnector, type ConnectorInput } from "@/lib/integrations";

type RouteParams = {
  params: Promise<{ firmId: string; connectorId: string }>;
};

// Proxies the Go backend's PATCH
// /api/firms/{firmId}/integration-connectors/{connectorId} (owner-only).
// No authorization decision made here, same convention as the sibling
// create route.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, connectorId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<ConnectorInput>;
  if (!body.name || !body.type) {
    return NextResponse.json({ error: "missing name or type" }, { status: 400 });
  }

  const result = await updateConnector(token, firmId, connectorId, {
    name: body.name,
    type: body.type,
    config: body.config ?? {},
    isActive: body.isActive ?? true,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.value);
}

// Proxies the Go backend's DELETE
// /api/firms/{firmId}/integration-connectors/{connectorId} (owner-only,
// hard delete - cascades to every mapping under it, migrations/0038's
// own ON DELETE CASCADE).
export async function DELETE(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, connectorId } = await params;
  const ok = await deleteConnector(token, firmId, connectorId);
  if (!ok) {
    return NextResponse.json({ error: "failed to delete connector" }, { status: 400 });
  }
  return NextResponse.json({ ok: true });
}
