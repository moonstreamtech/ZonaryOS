// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateMapping, deleteMapping, type MappingInput } from "@/lib/integrations";

type RouteParams = {
  params: Promise<{ firmId: string; mappingId: string }>;
};

// Proxies the Go backend's PATCH
// /api/firms/{firmId}/integration-mappings/{mappingId} (owner-only). No
// authorization decision made here, same convention as the sibling
// create route.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, mappingId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<MappingInput>;
  if (!body.connectorId || !body.sourceEntity || !body.targetEntity || !body.syncDirection) {
    return NextResponse.json({ error: "missing required mapping fields" }, { status: 400 });
  }

  const result = await updateMapping(token, firmId, mappingId, {
    connectorId: body.connectorId,
    sourceEntity: body.sourceEntity,
    targetEntity: body.targetEntity,
    fieldMappings: body.fieldMappings ?? [],
    syncDirection: body.syncDirection,
    isActive: body.isActive ?? true,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.value);
}

// Proxies the Go backend's DELETE
// /api/firms/{firmId}/integration-mappings/{mappingId} (owner-only, hard
// delete).
export async function DELETE(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, mappingId } = await params;
  const ok = await deleteMapping(token, firmId, mappingId);
  if (!ok) {
    return NextResponse.json({ error: "failed to delete mapping" }, { status: 400 });
  }
  return NextResponse.json({ ok: true });
}
