// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchProjects, createProject, type CreateProjectInput, type ProjectStatus } from "@/lib/project";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET/POST /api/firms/{firmId}/projects
// (optional ?status= filter on GET). POST is owner-only - no
// authorization decision made here: internal/project.CreateProject is
// the sole place that checks the caller actually holds an owner-flagged
// role, same convention as every other mutation proxy route in this
// codebase.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const status = (request.nextUrl.searchParams.get("status") as ProjectStatus | null) ?? undefined;
  const projects = await fetchProjects(token, firmId, { status });
  if (projects === null) {
    return NextResponse.json({ error: "load failed" }, { status: 502 });
  }
  return NextResponse.json(projects);
}

export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<CreateProjectInput>;
  if (!body.name) {
    return NextResponse.json({ error: "missing name" }, { status: 400 });
  }

  const result = await createProject(token, firmId, {
    name: body.name,
    description: body.description,
    startDate: body.startDate,
    endDate: body.endDate,
    budget: body.budget,
    currency: body.currency,
    managerPersonId: body.managerPersonId,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.project, { status: 201 });
}
