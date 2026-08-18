// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { suggestReport } from "@/lib/aiConfig";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/ai/suggest-report
// (owner-only) - same 200/404/422 forwarding contract as the sibling
// suggest-workflow route, for ReportBuilder's own "Ask AI" panel.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as { question?: string };
  if (!body.question) {
    return NextResponse.json({ error: "missing question" }, { status: 400 });
  }

  const result = await suggestReport(token, firmId, body.question);
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
