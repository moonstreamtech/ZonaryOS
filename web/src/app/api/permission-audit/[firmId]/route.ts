// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchFirmPermissionAudit } from "@/lib/permissionAudit";

// Proxies the Go backend's GET /api/firms/{firmId}/permission-audit -
// same cookie-to-Bearer-token pattern as every other proxy route.
export async function GET(
  request: NextRequest,
  { params }: { params: Promise<{ firmId: string }> },
) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const audit = await fetchFirmPermissionAudit(token, firmId);
  if (!audit) {
    return NextResponse.json({ error: "not available" }, { status: 403 });
  }

  return NextResponse.json(audit);
}
