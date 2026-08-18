// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createAIConfig, type CreateAIConfigInput } from "@/lib/aiConfig";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/ai-config (owner-only)
// - no authorization or validation decision made here: internal/ai.CreateConfig
// is the sole place that checks the caller is an owner and that the
// provider/model/apiKey are structurally valid, same convention as every
// other proxy route (e.g. src/app/api/apikeys/[firmId]/route.ts).
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateAIConfigInput>;
  if (!body.provider || !body.model || !body.apiKey) {
    return NextResponse.json({ error: "missing provider, model, or apiKey" }, { status: 400 });
  }

  const result = await createAIConfig(token, firmId, {
    provider: body.provider,
    model: body.model,
    apiKey: body.apiKey,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.config, { status: 201 });
}
