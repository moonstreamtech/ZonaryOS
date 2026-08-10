// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createReportDefinition, type ReportDefinitionInput } from "@/lib/reports";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/report-definitions -
// no authorization decision made here: internal/reports.
// CreateReportDefinition is the sole place that validates the query_spec
// and checks firm membership, same convention as every other proxy route.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<ReportDefinitionInput>;
  if (!body.name || !body.querySpec) {
    return NextResponse.json({ error: "missing name or querySpec" }, { status: 400 });
  }

  const result = await createReportDefinition(token, firmId, {
    name: body.name,
    description: body.description,
    querySpec: body.querySpec,
  });
  if (!result.ok) {
    return NextResponse.json({ error: "failed to create report definition" }, { status: result.status || 400 });
  }
  return NextResponse.json(result.definition, { status: 201 });
}
