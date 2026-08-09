// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { issueEdgeAgentToken } from "@/lib/edgeAgents";

type RouteParams = {
  params: Promise<{ firmId: string; agentId: string }>;
};

// Proxies the Go backend's POST .../edge-agents/{agentId}/tokens
// (owner-only) - no authorization decision made here, same convention as
// the sibling create-agent route. The response (including the one-time
// plaintext token) is returned straight through to the browser over
// HTTPS/the session's own TLS - it is never logged or persisted anywhere
// in this Next.js layer.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, agentId } = await params;
  const body = (await request.json().catch(() => ({}))) as { name?: string };
  if (!body.name) {
    return NextResponse.json({ error: "missing name" }, { status: 400 });
  }

  const result = await issueEdgeAgentToken(token, firmId, agentId, body.name);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json({ token: result.plaintext, summary: result.summary }, { status: 201 });
}
