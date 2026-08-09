// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchExport } from "@/lib/portability";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's GET /api/firms/{firmId}/export (owner-only) -
// no authorization decision made here, internal/portability.ExportFirm is
// the sole place that checks the caller actually holds an owner-flagged
// role, same convention as every other proxy route in this codebase. The
// backend's own Content-Disposition header is re-sent so the /settings
// "Export" button's plain navigation downloads a file rather than
// rendering raw JSON inline.
export async function GET(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const result = await fetchExport(token, firmId);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }

  return new NextResponse(result.body, {
    status: 200,
    headers: {
      "Content-Type": "application/json",
      "Content-Disposition": 'attachment; filename="zonaryos-export.json"',
    },
  });
}
