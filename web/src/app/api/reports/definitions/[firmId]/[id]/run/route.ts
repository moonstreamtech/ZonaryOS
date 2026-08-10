// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { runReport } from "@/lib/reports";

type RouteParams = {
  params: Promise<{ firmId: string; id: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/report-definitions/{id}/run
// - runs synchronously, result comes back directly in the response (see
// internal/reports.RunReport's own doc comment).
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, id } = await params;
  const result = await runReport(token, firmId, id);
  if (!result.ok) {
    return NextResponse.json({ error: "failed to run report" }, { status: result.status || 400 });
  }
  return NextResponse.json(result.rows);
}
