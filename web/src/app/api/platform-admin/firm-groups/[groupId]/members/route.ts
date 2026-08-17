// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { addGroupMember, type AddGroupMemberInput } from "@/lib/consolidation";

type RouteParams = {
  params: Promise<{ groupId: string }>;
};

// Proxies the Go backend's
// POST /api/platform-admin/firm-groups/{groupId}/members
// (internal/platformadmin.Allowlist-gated) - no authorization decision
// made here, same convention as every other platform-admin proxy route.
export async function POST(request: NextRequest, { params }: RouteParams) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { groupId } = await params;
  const body = (await request.json().catch(() => ({}))) as Partial<AddGroupMemberInput>;
  if (!body.firmId || !body.role) {
    return NextResponse.json({ error: "missing required field: firmId or role" }, { status: 400 });
  }

  const result = await addGroupMember(token, groupId, {
    firmId: body.firmId,
    role: body.role,
    ownershipPct: body.ownershipPct,
  });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.member, { status: 201 });
}
