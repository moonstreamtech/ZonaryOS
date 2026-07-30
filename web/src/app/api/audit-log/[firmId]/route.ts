// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchAuditLog } from "@/lib/auditlog";

// Proxies the Go backend's GET /api/firms/{firmId}/audit-log - same
// cookie-to-Bearer-token pattern as src/app/api/permission-audit/[firmId]/route.ts.
// No authorization decision is made here: internal/auditlog.List is the
// sole place that checks internal/auditlog.ReadPermission.
export async function GET(
  request: NextRequest,
  { params }: { params: Promise<{ firmId: string }> },
) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const entries = await fetchAuditLog(token, firmId);
  if (!entries) {
    return NextResponse.json({ error: "not available" }, { status: 403 });
  }

  return NextResponse.json(entries);
}
