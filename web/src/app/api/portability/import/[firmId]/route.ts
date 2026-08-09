// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { importConfiguration } from "@/lib/portability";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/import/configuration
// (owner-only) - no authorization decision made here, same convention as
// every other mutation proxy route in this codebase. The request body is
// forwarded byte-for-byte (the uploaded export file's own text), not
// re-parsed/re-serialized here, so this route never needs to know
// ExportDocument's shape either.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const documentJSON = await request.text();
  if (!documentJSON) {
    return NextResponse.json({ error: "missing request body" }, { status: 400 });
  }

  const result = await importConfiguration(token, firmId, documentJSON);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.summary, { status: 200 });
}
