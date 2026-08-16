// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchProjectSummary } from "@/lib/project";

type RouteParams = {
  params: Promise<{ firmId: string; projectId: string }>;
};

// Proxies the Go backend's GET
// /api/firms/{firmId}/projects/{projectId}/summary - the task/time/budget
// summary the project detail page renders. No authorization decision
// made here, same convention as the sibling project route.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, projectId } = await params;
  const summary = await fetchProjectSummary(token, firmId, projectId);
  if (summary === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(summary);
}
