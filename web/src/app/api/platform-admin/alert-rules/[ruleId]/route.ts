// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateAlertRule, deleteAlertRule, type AlertRuleInput } from "@/lib/alertRules";

type RouteParams = {
  params: Promise<{ ruleId: string }>;
};

// Proxies the Go backend's PATCH /api/platform-admin/alert-rules/{ruleId}
// (internal/platformadmin.Allowlist-gated) - no authorization decision
// made here, same convention as every other platform-admin proxy route.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { ruleId } = await params;
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

  const result = await updateAlertRule(token, ruleId, {
    name: body.name,
    metric: body.metric,
    threshold: body.threshold,
    windowMinutes: body.windowMinutes,
    isActive: body.isActive,
  });
  if (!result.ok) {
    return NextResponse.json({ error: "failed to update alert rule" }, { status: result.status || 400 });
  }
  return NextResponse.json(result.rule);
}

// Proxies the Go backend's DELETE /api/platform-admin/alert-rules/{ruleId}
// - same no-authorization-decision-here convention as PATCH above.
export async function DELETE(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { ruleId } = await params;
  const result = await deleteAlertRule(token, ruleId);
  if (!result.ok) {
    return NextResponse.json({ error: "failed to delete alert rule" }, { status: result.status || 400 });
  }
  return new NextResponse(null, { status: 204 });
}
