// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createRole } from "@/lib/permissionAudit";

type RouteParams = {
  params: Promise<{ firmId: string }>;
};

// Proxies the Go backend's POST /api/firms/{firmId}/roles (item 5's
// role-creation mechanism) - same cookie-to-Bearer-token pattern as
// every other proxy route, no authorization decision made here: the
// backend's CreateRole is the sole place that checks the caller actually
// holds an owner-flagged role.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId } = await params;
  const body = (await request.json().catch(() => ({}))) as {
    key?: string;
    name?: string;
  };
  if (!body.key || !body.name) {
    return NextResponse.json({ error: "missing key or name" }, { status: 400 });
  }

  const result = await createRole(token, firmId, body.key, body.name);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json({ roleId: result.roleId }, { status: 201 });
}
