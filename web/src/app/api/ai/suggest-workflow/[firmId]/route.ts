// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { suggestWorkflow } from "@/lib/aiConfig";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/ai/suggest-workflow
// (owner-only) - forwards the three-way 200/404/422 outcome verbatim
// (see lib/aiConfig.ts's own AISuggestResult doc comment) rather than
// collapsing 404/422 into a generic error, since DefinitionBuilder needs
// to tell "AI isn't configured" apart from "the AI's suggestion couldn't
// be used" to show the right inline message.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as { description?: string };
  if (!body.description) {
    return NextResponse.json({ error: "missing description" }, { status: 400 });
  }

  const result = await suggestWorkflow(token, firmId, body.description);
  switch (result.kind) {
    case "ok":
      return NextResponse.json(result.suggestion, { status: 200 });
    case "notConfigured":
      return NextResponse.json({ error: "not configured" }, { status: 404 });
    case "invalid":
      return new NextResponse(result.raw, { status: 422 });
    case "error":
      return NextResponse.json({ error: result.message }, { status: 400 });
  }
}
