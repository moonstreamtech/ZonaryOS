// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { createFirmGroup, type CreateFirmGroupInput } from "@/lib/consolidation";

// Proxies the Go backend's POST /api/platform-admin/firm-groups
// (internal/platformadmin.Allowlist-gated) - no authorization decision
// made here, same convention as every other platform-admin proxy route
// (see api/platform-admin/exchange-rates/route.ts). There is no GET here:
// reads happen server-side, directly against the backend, from
// platform-admin/firm-groups/page.tsx itself (same pattern
// platform-admin/exchange-rates/page.tsx already uses for its own list).
export async function POST(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const body = (await request.json().catch(() => ({}))) as Partial<CreateFirmGroupInput>;
  if (!body.name) {
    return NextResponse.json({ error: "missing required field: name" }, { status: 400 });
  }

  const result = await createFirmGroup(token, { name: body.name });
  if (!result.ok) {
    return NextResponse.json({ error: result.error }, { status: result.status || 400 });
  }
  return NextResponse.json(result.group, { status: 201 });
}
