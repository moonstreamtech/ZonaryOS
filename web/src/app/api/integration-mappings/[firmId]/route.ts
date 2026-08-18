// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchMappings, createMapping, type MappingInput } from "@/lib/integrations";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET
// /api/firms/{firmId}/integration-mappings (member-only), optionally
// filtered to one connector via ?connectorId= - fetched client-side
// (unlike the connector list, which loads server-side in page.tsx)
// because the mapping list is scoped to whichever connector the user has
// selected in the UI, same reasoning as
// src/app/api/webhooks/[firmId]/[webhookId]/deliveries/route.ts's own
// doc comment.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const connectorId = request.nextUrl.searchParams.get("connectorId") ?? undefined;
  const mappings = await fetchMappings(token, firmId, connectorId);
  if (mappings === null) {
    return NextResponse.json({ error: "failed to load mappings" }, { status: 400 });
  }
  return NextResponse.json(mappings);
}

// Proxies the Go backend's POST /api/firms/{firmId}/integration-mappings
// (owner-only) - no authorization or field-mapping/transform validation
// decision made here: internal/integration.CreateMapping is the sole
// place that checks the caller is an owner and that every field mapping
// and its transform spec are structurally valid.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<MappingInput>;
  if (!body.connectorId || !body.sourceEntity || !body.targetEntity || !body.syncDirection) {
    return NextResponse.json({ error: "missing required mapping fields" }, { status: 400 });
  }

  const result = await createMapping(token, firmId, {
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
  return NextResponse.json(result.value, { status: 201 });
}
