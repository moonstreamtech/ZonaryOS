// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { updateFirmName } from "@/lib/firm";

// Proxies the Go backend's PATCH /api/firms/{firmId} (item 4) - the
// settings page's firm-name editor. No authorization decision is made
// here: internal/firm.UpdateName is the sole place that checks the
// caller is an owner.
export async function PATCH(request: NextRequest) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const body = (await request.json().catch(() => ({}))) as {
    firmId?: string;
    name?: string;
  };
  if (!body.firmId || !body.name) {
    return NextResponse.json({ error: "missing firmId or name" }, { status: 400 });
  }

  const result = await updateFirmName(token, body.firmId, body.name);
  if (!result.ok) {
    return NextResponse.json(
      { error: result.error },
      { status: result.status || 400 },
    );
  }

  return NextResponse.json({ name: result.name });
}
