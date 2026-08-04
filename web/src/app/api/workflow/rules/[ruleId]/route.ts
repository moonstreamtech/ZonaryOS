// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateRule, deleteRule, type RuleUpdateInput } from "@/lib/workflow";

// Proxies the Go backend's PATCH .../workflow-definitions/{definitionKey}/rules/{ruleId}
// (the builder's edit/toggle action). No authorization decision is made
// here: internal/workflow.UpdateRuleForFirm is the sole place that checks
// the caller is an owner.
export async function PATCH(
  request: NextRequest,
  { params }: { params: Promise<{ ruleId: string }> },
) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { ruleId } = await params;
  const body = (await request.json().catch(() => ({}))) as {
    firmId?: string;
    definitionKey?: string;
    patch?: RuleUpdateInput;
  };
  if (!body.firmId || !body.definitionKey || !body.patch) {
    return NextResponse.json({ error: "missing firmId, definitionKey, or patch" }, { status: 400 });
  }

  const result = await updateRule(token, body.firmId, body.definitionKey, ruleId, body.patch);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }

  return NextResponse.json(result.rule);
}

// Proxies the Go backend's DELETE .../workflow-definitions/{definitionKey}/rules/{ruleId}
// - same no-authorization-decision-here convention as PATCH above
// (internal/workflow.DeleteRuleForFirm is the sole owner check).
export async function DELETE(
  request: NextRequest,
  { params }: { params: Promise<{ ruleId: string }> },
) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { ruleId } = await params;
  // Query params, not a JSON body: DELETE requests are conventionally
  // bodyless, and both values are already known to the caller from the
  // page it's rendering on (same convention firm/switch-adjacent DELETE-
  // shaped actions in this codebase avoid by using a body elsewhere, but
  // here there is genuinely nothing else to send).
  const firmId = request.nextUrl.searchParams.get("firmId");
  const definitionKey = request.nextUrl.searchParams.get("definitionKey");
  if (!firmId || !definitionKey) {
    return NextResponse.json({ error: "missing firmId or definitionKey" }, { status: 400 });
  }

  const result = await deleteRule(token, firmId, definitionKey, ruleId);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }

  return new NextResponse(null, { status: 204 });
}
