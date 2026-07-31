// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { renameRole, deleteRole } from "@/lib/permissionAudit";

type RouteParams = {
  params: Promise<{ firmId: string; roleId: string }>;
};

// Proxies the Go backend's PATCH/DELETE /api/firms/{firmId}/roles/{roleId}
// - same cookie-to-Bearer-token pattern as every other proxy route. No
// authorization decision is made here: the backend's RenameRole/
// DeleteRole are the sole place that checks the caller holds an
// owner-flagged role, and that the target role isn't itself
// owner-flagged.
export async function PATCH(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, roleId } = await params;
  const body = (await request.json().catch(() => ({}))) as { name?: string };
  if (!body.name) {
    return NextResponse.json({ error: "missing name" }, { status: 400 });
  }

  const result = await renameRole(token, firmId, roleId, body.name);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return new NextResponse(null, { status: 204 });
}

export async function DELETE(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { firmId, roleId } = await params;
  const result = await deleteRole(token, firmId, roleId);
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return new NextResponse(null, { status: 204 });
}
