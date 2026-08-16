// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchProject, updateProject, type UpdateProjectInput } from "@/lib/project";

type RouteParams = {
  params: Promise<{ firmId: string; projectId: string }>;
};

// Proxies the Go backend's GET/PATCH
// /api/firms/{firmId}/projects/{projectId}. PATCH is owner-only - no
// authorization decision made here: internal/project.UpdateProject is
// the sole place that checks the caller actually holds an owner-flagged
// role, same convention as every other mutation proxy route in this
// codebase.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, projectId } = await params;
  const project = await fetchProject(token, firmId, projectId);
  if (project === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(project);
}

export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, projectId } = await params;
  const body = (await request.json().catch(() => ({}))) as UpdateProjectInput;

  const result = await updateProject(token, firmId, projectId, body);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.project);
}
