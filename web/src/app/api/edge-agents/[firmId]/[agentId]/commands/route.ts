// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createEdgeAgentCommand, fetchEdgeAgentCommands, type CreateEdgeCommandInput } from "@/lib/edgeAgents";

type RouteParams = {
  params: Promise<{ firmId: string; agentId: string }>;
};

// Proxies the Go backend's POST .../edge-agents/{agentId}/commands
// (owner-only) - no authorization decision made here, same convention as
// the sibling tokens route: internal/edgeagent.CreateCommand is the sole
// place that checks the caller actually holds an owner-flagged role.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, agentId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateEdgeCommandInput>;
  if (!body.commandType) {
    return NextResponse.json({ error: "missing commandType" }, { status: 400 });
  }

  const result = await createEdgeAgentCommand(token, firmId, agentId, {
    commandType: body.commandType,
    payload: body.payload,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.command, { status: 201 });
}

// Proxies the Go backend's GET .../edge-agents/{agentId}/commands
// (member-gated) - the command history section of the edge agent detail
// page, same "no authorization decision made here" convention as the
// sibling events route.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, agentId } = await params;
  const commands = await fetchEdgeAgentCommands(token, firmId, agentId);
  if (commands === null) {
    return NextResponse.json({ error: "failed to load commands" }, { status: 502 });
  }
  return NextResponse.json(commands);
}
