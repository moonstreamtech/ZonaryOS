// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createAPIKey, type CreateAPIKeyInput } from "@/lib/apikeys";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/api-keys (owner-only)
// - no authorization or scope-validation decision made here:
// internal/apikey.CreateAPIKey is the sole place that checks the caller
// is an owner and that requested scopes never exceed their own granted
// permissions, same convention as every other proxy route.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateAPIKeyInput>;
  if (!body.name) {
    return NextResponse.json({ error: "missing name" }, { status: 400 });
  }

  const result = await createAPIKey(token, firmId, {
    name: body.name,
    scopes: body.scopes ?? [],
    expiresAt: body.expiresAt,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.apiKey, { status: 201 });
}
