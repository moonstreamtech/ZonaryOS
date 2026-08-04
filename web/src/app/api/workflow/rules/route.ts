// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createRule, type RuleInput } from "@/lib/workflow";

// Proxies the Go backend's POST .../workflow-definitions/{definitionKey}/rules
// (the Rule Engine Builder UI's create action). No authorization decision
// is made here: internal/workflow.CreateRuleForFirm is the sole place
// that checks the caller is an owner - this route only forwards the
// caller's session token and the rule they built.
export async function POST(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const body = (await request.json().catch(() => ({}))) as {
    firmId?: string;
    definitionKey?: string;
    rule?: RuleInput;
  };
  if (!body.firmId || !body.definitionKey || !body.rule) {
    return NextResponse.json({ error: "missing firmId, definitionKey, or rule" }, { status: 400 });
  }

  const result = await createRule(token, body.firmId, body.definitionKey, body.rule);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }

  return NextResponse.json(result.rule, { status: 201 });
}
