// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

import { NextRequest, NextResponse } from "next/server";
import { fetchWizardNode } from "@/lib/wizard";

// Proxies the Go backend's GET /api/wizard/nodes/{nodeKey}, forwarding the
// caller's session token as a Bearer token - same pattern as
// src/app/api/me/route.ts. The httpOnly session cookie never needs to
// reach client-side JS: the wizard's client component calls this route
// instead of the backend directly.
export async function GET(
  request: NextRequest,
  { params }: { params: Promise<{ nodeKey: string }> },
) {
  const token = request.cookies.get("zonaryos_session")?.value;
  if (!token) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }

  const { nodeKey } = await params;
  const node = await fetchWizardNode(token, nodeKey);
  if (!node) {
    return NextResponse.json({ error: "not found" }, { status: 404 });
  }

  return NextResponse.json(node);
}
