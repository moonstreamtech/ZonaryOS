// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchAlertRules, createAlertRule, type AlertRuleInput } from "@/lib/alertRules";

// Proxies the Go backend's GET/POST /api/platform-admin/alert-rules
// (internal/platformadmin.Allowlist-gated) - no authorization decision
// made here, same convention as every other platform-admin proxy route
// (see api/platform-admin/exchange-rates/route.ts and
// api/platform-admin/firm-groups/route.ts).
export async function GET(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const rules = await fetchAlertRules(token);
  if (rules === null) {
    return NextResponse.json({ error: "failed to load alert rules" }, { status: 404 });
  }
  return NextResponse.json(rules);
}

export async function POST(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const body = (await request.json().catch(() => ({}))) as Partial<AlertRuleInput>;
  if (
    !body.name ||
    !body.metric ||
    body.threshold === undefined ||
    body.windowMinutes === undefined ||
    body.isActive === undefined
  ) {
    return NextResponse.json(
      { error: "missing required field: name, metric, threshold, windowMinutes, or isActive" },
      { status: 400 },
    );
  }

  const result = await createAlertRule(token, {
    name: body.name,
    metric: body.metric,
    threshold: body.threshold,
    windowMinutes: body.windowMinutes,
    isActive: body.isActive,
  });
  if (!result.ok) {
    return NextResponse.json({ error: "failed to create alert rule" }, { status: result.status || 400 });
  }
  return NextResponse.json(result.rule, { status: 201 });
}
