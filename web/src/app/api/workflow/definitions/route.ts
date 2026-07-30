// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { defineWorkflow, type DefinitionSpecInput } from "@/lib/workflow";

// Proxies the Go backend's POST .../workflow-definitions (item 1) - the
// workflow definition builder's submit action. No authorization decision
// is made here: internal/workflow.DefineWorkflowForFirm is the sole place
// that checks the caller is an owner - this route only forwards the
// caller's session token and the spec they built.
export async function POST(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const body = (await request.json().catch(() => ({}))) as {
    firmId?: string;
    spec?: DefinitionSpecInput;
  };
  if (!body.firmId || !body.spec) {
    return NextResponse.json({ error: "missing firmId or spec" }, { status: 400 });
  }

  const result = await defineWorkflow(token, body.firmId, body.spec);
  if (!result.ok) {
    return NextResponse.json(
      { error: result.error },
      { status: result.status || 400 },
    );
  }

  return NextResponse.json(result.definition, { status: 201 });
}
